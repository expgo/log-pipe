package logpipe

type Subscriber struct {
	ch       chan string
	done     chan struct{}
	doneFunc func()
}

func (s *Subscriber) Close() {
	s.doneFunc()
	close(s.ch)
}

func (s *Subscriber) Channel() <-chan string {
	return s.ch
}
