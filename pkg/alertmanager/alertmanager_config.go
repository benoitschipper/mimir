// This file contains code for the migration from classic mode to UTF-8 mode
// in the Alertmanager. It is intended to be used to gather data about
// configurations that are incompatible with the UTF-8 matchers parser, so
// action can be taken to fix those configurations.

package alertmanager

import (
	"github.com/go-kit/log"
	"github.com/prometheus/alertmanager/matchers/compat"
	"gopkg.in/yaml.v3"

	"github.com/grafana/mimir/pkg/alertmanager/alertspb"
)

// basicConfig is a basic version of an Alertmanager configuration containing
// just the configuration options that are matchers. basicConfig is used to
// validate that existing configurations are forwards compatible with the new
// UTF-8 parser in Alertmanager (see the matchers/parse package).
type basicConfig struct {
	Route        *basicRoute         `yaml:"route,omitempty" json:"route,omitempty"`
	InhibitRules []*basicInhibitRule `yaml:"inhibit_rules,omitempty" json:"inhibit_rules,omitempty"`
}

type basicRoute struct {
	Matchers []string      `yaml:"matchers,omitempty" json:"matchers,omitempty"`
	Routes   []*basicRoute `yaml:"routes,omitempty" json:"routes,omitempty"`
}

type basicInhibitRule struct {
	SourceMatchers []string `yaml:"source_matchers,omitempty" json:"source_matchers,omitempty"`
	TargetMatchers []string `yaml:"target_matchers,omitempty" json:"target_matchers,omitempty"`
}

// ValidateMatchersInConfig validates that an existing configuration is forwards
// compatible with the new UTF-8 parser in Alertmanager (see the matchers/parse
// package). It returns an error if the configuration is invalid YAML or cannot
// be decoded into basicConfig. It does not return errors for configurations that
// are not forwards compatible, and instead emits logs and increments metrics for
// each incompatible input encountered.
func validateMatchersInConfig(logger log.Logger, metrics *compat.Metrics, origin string, cfg alertspb.AlertConfigDesc) error {
	var (
		parseFn  compat.MatchersParser
		basicCfg basicConfig
		err      error
	)
	logger = log.With(logger, "user", cfg.User)
	parseFn = compat.FallbackMatchersParser(logger, metrics)
	if err = yaml.Unmarshal([]byte(cfg.RawConfig), &basicCfg); err != nil {
		return err
	}
	if err = validateRoute(logger, parseFn, origin, basicCfg.Route, cfg.User); err != nil {
		return err
	}
	if err = validateInhibitionRules(logger, parseFn, origin, basicCfg.InhibitRules, cfg.User); err != nil {
		return err
	}
	return nil
}

func validateRoute(logger log.Logger, parseFn compat.MatchersParser, origin string, r *basicRoute, user string) error {
	if r == nil {
		return nil
	}
	for _, m := range r.Matchers {
		parseFn(m, origin)
	}
	for _, route := range r.Routes {
		if err := validateRoute(logger, parseFn, origin, route, user); err != nil {
			return err
		}
	}
	return nil
}

func validateInhibitionRules(_ log.Logger, parseFn compat.MatchersParser, origin string, rules []*basicInhibitRule, _ string) error {
	for _, r := range rules {
		for _, m := range r.SourceMatchers {
			parseFn(m, origin)
		}
		for _, m := range r.TargetMatchers {
			parseFn(m, origin)
		}
	}
	return nil
}
