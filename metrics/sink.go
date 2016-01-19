package metrics

type Sink interface {
	Handle(metric string, tags Tags, value float64, metricType metricType) error
	Flush() error
	Close()
}

type nullSink struct{}

func (sink *nullSink) Handle(metric string, tags Tags, value float64, metricType metricType) error {
	return nil
}

func (sink *nullSink) Flush() error {
	return nil
}

func (sink *nullSink) Close() {
}

var NullSink Sink = &nullSink{}
