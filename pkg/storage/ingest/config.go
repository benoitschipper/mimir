// SPDX-License-Identifier: AGPL-3.0-only

package ingest

import (
	"errors"
	"flag"
	"time"
)

var (
	ErrMissingKafkaAddress = errors.New("the Kafka address has not been configured")
	ErrMissingKafkaTopic   = errors.New("the Kafka topic has not been configured")
)

type Config struct {
	Enabled     bool        `yaml:"enabled"`
	Zone        string      `yaml:"availability_zone"`
	KafkaConfig KafkaConfig `yaml:"kafka"`
}

func (cfg *Config) RegisterFlags(f *flag.FlagSet) {
	f.BoolVar(&cfg.Enabled, "ingest-storage.enabled", false, "True to enable the ingestion via object storage.")
	f.StringVar(&cfg.Zone, "ingest-storage.availability-zone", "", "The availability zone of the current instance. This option is used by queriers when selecting which ingesters to query. It should match the -ring.instance-availability-zone value of the ingester zone.")

	cfg.KafkaConfig.RegisterFlagsWithPrefix("ingest-storage.kafka", f)
}

// Validate the config.
func (cfg *Config) Validate() error {
	// Skip validation if disabled.
	if !cfg.Enabled {
		return nil
	}

	if err := cfg.KafkaConfig.Validate(); err != nil {
		return err
	}

	return nil
}

// KafkaConfig holds the generic config for the Kafka backend.
type KafkaConfig struct {
	Address      string        `yaml:"address"`
	Topic        string        `yaml:"topic"`
	ClientID     string        `yaml:"client_id"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`

	LastProducedOffsetPollInterval time.Duration `yaml:"last_produced_offset_poll_interval"`
	LastProducedOffsetRetryTimeout time.Duration `yaml:"last_produced_offset_retry_timeout"`
}

func (cfg *KafkaConfig) RegisterFlags(f *flag.FlagSet) {
	cfg.RegisterFlagsWithPrefix("", f)
}

func (cfg *KafkaConfig) RegisterFlagsWithPrefix(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.Address, prefix+".address", "", "The Kafka backend address.")
	f.StringVar(&cfg.Topic, prefix+".topic", "", "The Kafka topic name.")
	f.StringVar(&cfg.ClientID, prefix+".client-id", "", "The Kafka client ID.")
	f.DurationVar(&cfg.DialTimeout, prefix+".dial-timeout", 2*time.Second, "The maximum time allowed to open a connection to a Kafka broker.")
	f.DurationVar(&cfg.WriteTimeout, prefix+".write-timeout", 10*time.Second, "How long to wait for an incoming write request to be successfully committed to the Kafka backend.")

	f.DurationVar(&cfg.LastProducedOffsetPollInterval, prefix+".last-produced-offset-poll-interval", time.Second, "How frequently to poll the last produced offset, used to enforce strong read consistency.")
	f.DurationVar(&cfg.LastProducedOffsetRetryTimeout, prefix+".last-produced-offset-retry-timeout", 10*time.Second, "How long to retry a failed request to get the last produced offset.")
}

func (cfg *KafkaConfig) Validate() error {
	if cfg.Address == "" {
		return ErrMissingKafkaAddress
	}
	if cfg.Topic == "" {
		return ErrMissingKafkaTopic
	}

	return nil
}
