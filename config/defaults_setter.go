package config

// DefaultsSetter is satisfied by *viper.Viper and any config framework that supports
// programmatic defaults. Implementing this interface requires no external imports,
// so this package does not need a direct Viper dependency.
type DefaultsSetter interface {
	SetDefault(key string, value any)
}

// SetManagerConfigDefaults registers all non-zero ManagerConfig default values with v.
// Call this before viper.Unmarshal so fields absent from the config file or environment
// retain their defaults — Viper's defaults survive struct decoding even for nested structs.
func SetManagerConfigDefaults(v DefaultsSetter) {
	v.SetDefault("cacheKeyPrefix", DefaultKeyPrefix)
	v.SetDefault("metricsPrefix", DefaultMetricsPrefix)
	v.SetDefault("location", DefaultLocation)
	v.SetDefault("coordinatorDrainTimeoutMs", DefaultCoordinatorDrainTimeoutMs)
	v.SetDefault("channelManagerDrainTimeoutMs", DefaultChannelManagerDrainTimeoutMs)
	v.SetDefault("socketModeMaxWorkers", DefaultSocketModeMaxWorkers)
	v.SetDefault("socketModeDrainTimeoutMs", DefaultSocketModeDrainTimeoutMs)
	setSlackClientConfigDefaults(v, "slackClient")
}

// SetAPIConfigDefaults registers all non-zero APIConfig default values with v.
// Call this before viper.Unmarshal so fields absent from the config file or environment
// retain their defaults.
func SetAPIConfigDefaults(v DefaultsSetter) {
	v.SetDefault("logJson", true)
	v.SetDefault("restPort", "8080")
	v.SetDefault("cacheKeyPrefix", DefaultKeyPrefix)
	v.SetDefault("metricsPrefix", DefaultMetricsPrefix)
	v.SetDefault("maxUsersInAlertChannel", 100)
	v.SetDefault("rateLimitPerAlertChannel.alertsPerSecond", 1.0)
	v.SetDefault("rateLimitPerAlertChannel.allowedBurst", 30)
	v.SetDefault("shutdownTimeoutMs", DefaultShutdownTimeoutMs)
	setSlackClientConfigDefaults(v, "slackClient")
}

// setSlackClientConfigDefaults registers SlackClientConfig defaults under the given key prefix.
func setSlackClientConfigDefaults(v DefaultsSetter, prefix string) {
	key := func(s string) string { return prefix + "." + s }
	v.SetDefault(key("concurrency"), DefaultConcurrency)
	v.SetDefault(key("maxAttemptsForRateLimitError"), DefaultMaxAttemptsForRateLimitError)
	v.SetDefault(key("maxAttemptsForTransientError"), DefaultMaxAttemptsForTransientError)
	v.SetDefault(key("maxRateLimitErrorWaitTimeSeconds"), DefaultMaxRateLimitErrorWaitTimeSeconds)
	v.SetDefault(key("maxTransientErrorWaitTimeSeconds"), DefaultMaxTransientErrorWaitTimeSeconds)
	v.SetDefault(key("httpTimeoutSeconds"), DefaultHTTPTimeoutSeconds)
}
