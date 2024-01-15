// SPDX-License-Identifier: AGPL-3.0-only

package querymiddleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/tenant"
	jsoniter "github.com/json-iterator/go"
	"github.com/klauspost/compress/s2"
	"github.com/opentracing/opentracing-go"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"golang.org/x/sync/errgroup"

	apierror "github.com/grafana/mimir/pkg/api/error"
	"github.com/grafana/mimir/pkg/querier/stats"
	"github.com/grafana/mimir/pkg/storage/sharding"
	"github.com/grafana/mimir/pkg/util"
	"github.com/grafana/mimir/pkg/util/spanlogger"
)

const encodingTypeSnappyFramed = "x-snappy-framed"

var errShardCountTooLow = errors.New("shard count too low")

type shardActiveSeriesMiddleware struct {
	upstream http.RoundTripper
	limits   Limits
	logger   log.Logger
	encoder  *s2.Writer
}

func newShardActiveSeriesMiddleware(upstream http.RoundTripper, limits Limits, logger log.Logger) http.RoundTripper {
	return &shardActiveSeriesMiddleware{
		upstream: upstream,
		limits:   limits,
		logger:   logger,
		encoder:  s2.NewWriter(nil),
	}
}

func (s *shardActiveSeriesMiddleware) RoundTrip(r *http.Request) (*http.Response, error) {
	spanLog, ctx := spanlogger.NewWithLogger(r.Context(), s.logger, "shardActiveSeries.RoundTrip")
	defer spanLog.Finish()

	tenantID, err := tenant.TenantID(ctx)
	if err != nil {
		return nil, apierror.New(apierror.TypeBadData, err.Error())
	}

	defaultShardCount := s.limits.QueryShardingTotalShards(tenantID)
	shardCount := setShardCountFromHeader(defaultShardCount, r, spanLog)

	if shardCount < 2 {
		spanLog.DebugLog("msg", "query sharding disabled for request")
		return s.upstream.RoundTrip(r)
	}

	if maxShards := s.limits.QueryShardingMaxShardedQueries(tenantID); shardCount > maxShards {
		return nil, apierror.New(
			apierror.TypeBadData,
			fmt.Sprintf("shard count %d exceeds allowed maximum (%d)", shardCount, maxShards),
		)
	}

	selector, err := parseSelector(r)
	if err != nil {
		return nil, apierror.New(apierror.TypeBadData, err.Error())
	}

	spanLog.DebugLog(
		"msg", "sharding active series query",
		"shardCount", shardCount, "selector", selector.String(),
	)

	reqs, err := buildShardedRequests(ctx, r, shardCount, selector)
	if err != nil {
		return nil, apierror.New(apierror.TypeInternal, err.Error())
	}

	resp, err := doShardedRequests(ctx, reqs, s.upstream)
	if err != nil {
		if errors.Is(err, errShardCountTooLow) {
			return nil, apierror.New(apierror.TypeBadData, fmt.Errorf("%w: try increasing the requested shard count", err).Error())
		}
		return nil, apierror.New(apierror.TypeInternal, err.Error())
	}

	return s.mergeResponses(ctx, resp, r.Header.Get("Accept-Encoding")), nil
}

func setShardCountFromHeader(origShardCount int, r *http.Request, spanLog *spanlogger.SpanLogger) int {
	for _, value := range r.Header.Values(totalShardsControlHeader) {
		shards, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			continue
		}
		if shards >= 0 {
			spanLog.DebugLog("msg", fmt.Sprintf("using shard count from header %s: %d", totalShardsControlHeader, shards))
			return int(shards)
		}
	}
	return origShardCount
}

func parseSelector(req *http.Request) (*parser.VectorSelector, error) {
	values, err := util.ParseRequestFormWithoutConsumingBody(req)
	if err != nil {
		return nil, err
	}
	valSelector := values.Get("selector")
	if valSelector == "" {
		return nil, errors.New("selector parameter is required")
	}
	parsed, err := parser.ParseExpr(valSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid selector: %w", err)
	}
	selector, ok := parsed.(*parser.VectorSelector)
	if !ok {
		return nil, fmt.Errorf("invalid selector: %w", err)
	}

	return selector, nil
}

func buildShardedRequests(ctx context.Context, req *http.Request, numRequests int, selector parser.Expr) ([]*http.Request, error) {
	reqs := make([]*http.Request, numRequests)
	for i := 0; i < numRequests; i++ {
		reqs[i] = req.Clone(ctx)

		sharded, err := shardedSelector(numRequests, i, selector)
		if err != nil {
			return nil, err
		}

		vals := url.Values{}
		vals.Set("selector", sharded.String())

		// Remove headers that we have consumed ourselves.
		reqs[i].Header.Del(totalShardsControlHeader)
		reqs[i].Header.Del("Accept-Encoding")

		// Populate the request with the shard selector.
		req.Form = nil
		req.PostForm = nil
		switch req.Method {
		case http.MethodPost:
			reqs[i].URL.RawQuery = ""
			reqs[i].Body = io.NopCloser(strings.NewReader(vals.Encode()))
		case http.MethodGet:
			reqs[i].URL.RawQuery = vals.Encode()
		default:
			return nil, fmt.Errorf("unsupported method: %s", req.Method)
		}
	}

	return reqs, nil
}

func doShardedRequests(ctx context.Context, upstreamRequests []*http.Request, next http.RoundTripper) ([]*http.Response, error) {
	resps := make([]*http.Response, len(upstreamRequests))

	g, ctx := errgroup.WithContext(ctx)
	queryStats := stats.FromContext(ctx)
	for i, req := range upstreamRequests {
		i, r := i, req
		g.Go(func() error {
			partialStats, childCtx := stats.ContextWithEmptyStats(ctx)
			partialStats.AddShardedQueries(1)

			var span opentracing.Span
			span, childCtx = opentracing.StartSpanFromContext(childCtx, "shardActiveSeries.doShardedRequest")
			defer span.Finish()

			resp, err := next.RoundTrip(r.WithContext(childCtx))
			if err != nil {
				span.LogFields(otlog.Error(err))
				return err
			}

			if resp.StatusCode != http.StatusOK {
				span.LogFields(otlog.Int("statusCode", resp.StatusCode))
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode == http.StatusRequestEntityTooLarge {
					return errShardCountTooLow
				}
				return fmt.Errorf("received unexpected response from upstream: status %d, body: %s", resp.StatusCode, string(body))
			}

			resps[i] = resp

			span.LogFields(otlog.Uint64("seriesCount", partialStats.LoadFetchedSeries()))
			queryStats.Merge(partialStats)

			return nil
		})
	}

	err := g.Wait()
	if err != nil {
		// If there was an error, we need to read and close all response bodies.
		for _, resp := range resps {
			if resp != nil {
				_, _ = io.ReadAll(resp.Body)
				_ = resp.Body.Close()
			}
		}
	}

	return resps, err
}

func shardedSelector(shardCount, currentShard int, expr parser.Expr) (parser.Expr, error) {
	originalSelector, ok := expr.(*parser.VectorSelector)
	if !ok {
		return nil, errors.New("invalid selector")
	}

	shardMatcher, err := labels.NewMatcher(
		labels.MatchEqual, sharding.ShardLabel,
		sharding.ShardSelector{ShardIndex: uint64(currentShard), ShardCount: uint64(shardCount)}.LabelValue(),
	)
	if err != nil {
		return nil, err
	}

	return &parser.VectorSelector{
		Name:          originalSelector.Name,
		LabelMatchers: append([]*labels.Matcher{shardMatcher}, originalSelector.LabelMatchers...),
	}, nil
}

func (s *shardActiveSeriesMiddleware) mergeResponses(ctx context.Context, responses []*http.Response, acceptEncoding string) *http.Response {
	reader, writer := io.Pipe()

	items := make(chan map[string]string)

	g := new(errgroup.Group)
	for _, res := range responses {
		if res == nil {
			continue
		}
		r := res
		g.Go(func() error {
			defer func(body io.ReadCloser) {
				_, _ = io.ReadAll(body)
				_ = body.Close()
			}(r.Body)

			it := jsoniter.Parse(jsoniter.ConfigFastest, r.Body, 512)

			// Iterate over fields until we find data or error fields
			foundDataField := false
			for it.Error == nil {
				field := it.ReadObject()
				if field == "error" {
					return fmt.Errorf("error in partial response: %s", it.ReadString())
				}
				if field == "data" {
					foundDataField = true
					break
				}
				// If the field is neither data nor error, we skip it.
				it.ReadAny()
			}
			if !foundDataField {
				return fmt.Errorf("expected data field at top level, found %s", it.CurrentBuffer())
			}

			if it.WhatIsNext() != jsoniter.ArrayValue {
				err := errors.New("expected data field to contain an array")
				return err
			}

			for it.ReadArray() {
				item := make(map[string]string)
				it.ReadMapCB(func(iterator *jsoniter.Iterator, s string) bool {
					item[s] = iterator.ReadString()
					return true
				})
				items <- item
			}

			return it.Error
		})
	}

	go func() {
		// We ignore the error from the errgroup because it will be checked again later.
		_ = g.Wait()
		close(items)
	}()

	response := &http.Response{Body: reader, StatusCode: http.StatusOK, Header: http.Header{}}
	response.Header.Set("Content-Type", "application/json")
	if acceptEncoding == encodingTypeSnappyFramed {
		response.Header.Set("Content-Encoding", encodingTypeSnappyFramed)
	}

	go s.writeMergedResponse(ctx, g.Wait, writer, items, acceptEncoding)

	return response
}

func (s *shardActiveSeriesMiddleware) writeMergedResponse(ctx context.Context, check func() error, w io.WriteCloser, items chan map[string]string, encodingType string) {
	defer func(encoder, w io.Closer) {
		_ = encoder.Close()
		_ = w.Close()
	}(s.encoder, w)

	span, _ := opentracing.StartSpanFromContext(ctx, "shardActiveSeries.writeMergedResponse")
	defer span.Finish()

	var out io.Writer = w
	if encodingType == encodingTypeSnappyFramed {
		span.LogFields(otlog.String("encoding", encodingTypeSnappyFramed))
		out = s.encoder
		s.encoder.Reset(w)
	} else {
		span.LogFields(otlog.String("encoding", "none"))
	}

	stream := jsoniter.NewStream(jsoniter.ConfigFastest, out, 512)
	defer func(stream *jsoniter.Stream) {
		_ = stream.Flush()
	}(stream)

	stream.WriteObjectStart()
	stream.WriteObjectField("data")
	stream.WriteArrayStart()
	firstItem := true
	for item := range items {
		if firstItem {
			firstItem = false
		} else {
			stream.WriteMore()
		}
		stream.WriteObjectStart()
		firstField := true
		for k, v := range item {
			if firstField {
				firstField = false
			} else {
				stream.WriteMore()
			}
			stream.WriteObjectField(k)
			stream.WriteString(v)
		}
		stream.WriteObjectEnd()
	}
	stream.WriteArrayEnd()

	if err := check(); err != nil {
		level.Error(s.logger).Log("msg", "error merging partial responses", "err", err.Error())
		span.LogFields(otlog.Error(err))
		stream.WriteMore()
		stream.WriteObjectField("status")
		stream.WriteString("error")
		stream.WriteMore()
		stream.WriteObjectField("error")
		stream.WriteString(fmt.Sprintf("error merging partial responses: %s", err.Error()))
	}

	stream.WriteObjectEnd()
}
