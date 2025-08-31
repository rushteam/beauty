package tracing

type Config struct {
	ServiceName     string
	ServiceVersion  string
	ServiceInstance string
	Environment     string  // dev, test, prod
	Endpoint        string  // host:port
	Authorization   string  // basic auth api key
	SampleRate      float64 // sample rate default is 1
}
