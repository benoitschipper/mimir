local utils = import 'mixin-utils/utils.libsonnet';
local filename = 'mimir-remote-ruler-reads.json';

(import 'dashboard-utils.libsonnet') +
(import 'dashboard-queries.libsonnet') {
  // Both support gRPC and HTTP requests. HTTP request is used when rule evaluation query requests go through the query-tee.
  local rulerRoutesRegex = '/httpgrpc.HTTP/Handle|.*api_v1_query',

  [filename]:
    ($.dashboard('Remote ruler reads') + { uid: std.md5(filename) })
    .addClusterSelectorTemplates()
    .addRowIf(
      $._config.show_dashboard_descriptions.reads,
      ($.row('Remote ruler reads dashboard description') { height: '175px', showTitle: false })
      .addPanel(
        $.textPanel('', |||
          <p>
            This dashboard shows health metrics for the ruler read path when remote operational mode is enabled.
            It is broken into sections for each service on the ruler read path, and organized by the order in which the read request flows.
            <br/>
            For each service, there are three panels showing (1) requests per second to that service, (2) average, median, and p99 latency of requests to that service, and (3) p99 latency of requests to each instance of that service.
          </p>
        |||),
      )
    )
    .addRow(
      ($.row('Headlines') +
       {
         height: '100px',
         showTitle: false,
       })
      .addPanel(
        $.panel('Evaluations / sec') +
        $.statPanel(|||
          sum(
            rate(
              cortex_request_duration_seconds_count{
                %(queryFrontend)s,
                route=~"%(rulerRoutesRegex)s"
              }[$__rate_interval]
            )
          )
        ||| % {
          queryFrontend: $.jobMatcher($._config.job_names.ruler_query_frontend),
          rulerRoutesRegex: rulerRoutesRegex,
        }, format='reqps') +
        $.panelDescription(
          'Evaluations per second',
          |||
            Rate of rule expressions evaluated per second.
          |||
        ),
      )
    )
    .addRow(
      $.row('Query-frontend (dedicated to ruler)')
      .addPanel(
        $.panel('Requests / sec') +
        $.qpsPanel('cortex_request_duration_seconds_count{%s, route=~"%s"}' % [$.jobMatcher($._config.job_names.ruler_query_frontend), rulerRoutesRegex])
      )
      .addPanel(
        $.panel('Latency') +
        utils.latencyRecordingRulePanel('cortex_request_duration_seconds', $.jobSelector($._config.job_names.ruler_query_frontend) + [utils.selector.re('route', rulerRoutesRegex)])
      )
      .addPanel(
        $.timeseriesPanel('Per %s p99 latency' % $._config.per_instance_label) +
        $.hiddenLegendQueryPanel(
          'histogram_quantile(0.99, sum by(le, %s) (rate(cortex_request_duration_seconds_bucket{%s, route=~"%s"}[$__rate_interval])))' % [$._config.per_instance_label, $.jobMatcher($._config.job_names.ruler_query_frontend), rulerRoutesRegex], ''
        )
      )
    )
    .addRow(
      local description = |||
        <p>
          The query scheduler is an optional service that moves
          the internal queue from the query-frontend into a
          separate component.
          If this service is not deployed,
          these panels will show "No data."
        </p>
      |||;
      $.row('Query-scheduler (dedicated to ruler)')
      .addPanel(
        local title = 'Requests / sec';
        $.panel(title) +
        $.qpsPanel('cortex_query_scheduler_queue_duration_seconds_count{%s}' % $.jobMatcher($._config.job_names.ruler_query_scheduler)) +
        $.panelDescription(title, description),
      )
      .addPanel(
        local title = 'Latency (Time in Queue)';
        $.panel(title) +
        $.latencyPanel('cortex_query_scheduler_queue_duration_seconds', '{%s}' % $.jobMatcher($._config.job_names.ruler_query_scheduler)) +
        $.panelDescription(title, description),
      )
      .addPanel(
        local title = 'Latency (Time in Queue) by Queue Dimension';
        $.panel(title) +
        $.latencyPanelLabelBreakout(
          metricName='cortex_query_scheduler_queue_duration_seconds',
          selector='{%s}' % $.jobMatcher($._config.job_names.ruler_query_scheduler),
          labels=['additional_queue_dimensions'],
          labelReplaceArgSets=[
            {
              dstLabel: 'additional_queue_dimensions',
              replacement: 'none',
              srcLabel:
                'additional_queue_dimensions',
              regex: '^$',
            },
          ]
        ) +
        $.panelDescription(title, description),
      )
      .addPanel(
        local title = 'Queue length';

        $.timeseriesPanel(title) +
        $.hiddenLegendQueryPanel(
          'sum(min_over_time(cortex_query_scheduler_queue_length{%s}[$__interval]))' % [$.jobMatcher($._config.job_names.ruler_query_scheduler)],
          'Queue length'
        ) +
        $.panelDescription(title, description) + {
          fieldConfig+: {
            defaults+: {
              unit: 'queries',
            },
          },
        },
      )
    )
    .addRow(
      $.row('Querier (dedicated to ruler)')
      .addPanel(
        $.panel('Requests / sec') +
        $.qpsPanel('cortex_querier_request_duration_seconds_count{%s, route=~"%s"}' % [$.jobMatcher($._config.job_names.ruler_querier), $.queries.read_http_routes_regex])
      )
      .addPanel(
        $.panel('Latency') +
        utils.latencyRecordingRulePanel('cortex_querier_request_duration_seconds', $.jobSelector($._config.job_names.ruler_querier) + [utils.selector.re('route', $.queries.read_http_routes_regex)])
      )
      .addPanel(
        $.timeseriesPanel('Per %s p99 latency' % $._config.per_instance_label) +
        $.hiddenLegendQueryPanel(
          'histogram_quantile(0.99, sum by(le, %s) (rate(cortex_querier_request_duration_seconds_bucket{%s, route=~"%s"}[$__rate_interval])))' % [$._config.per_instance_label, $.jobMatcher($._config.job_names.ruler_querier), $.queries.read_http_routes_regex], ''
        )
      )
    )
    .addRowIf(
      $._config.autoscaling.ruler_querier.enabled,
      $.cpuAndMemoryBasedAutoScalingRow('Ruler-Querier'),
    ),
}
