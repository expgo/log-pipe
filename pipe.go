package logpipe

import (
	"context"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/expgo/log"
	"github.com/expgo/serve"
	"github.com/thejerf/suture/v4"
)

//go:generate ag

// @Singleton
type Pipe struct {
	log.InnerLog
	pipeReader  *os.File
	pipeWriter  *os.File
	token       suture.ServiceToken
	subscribers []*Subscriber
	mu          sync.RWMutex
}

func (p *Pipe) Init() {
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		p.L.Fatal("Failed to create pipe: %v", err)
	}

	p.pipeReader = pipeReader
	p.pipeWriter = pipeWriter

	log.SetPipeWriter(pipeWriter)
	p.L.Info("Log pipe initialized successfully")

	p.token = serve.AddServe("log pipe", p)
}

func (p *Pipe) Serve(ctx context.Context) error {
	p.L.Info("Log pipe service started")
	const maxCapacity = 1024 * 1024
	buf := make([]byte, maxCapacity)

	// 设置为非阻塞模式
	if err := setNonblock(p.pipeReader); err != nil {
		p.L.Errorf("Failed to set non-blocking mode: %v", err)
		return err
	}
	p.L.Debug("Set pipe to non-blocking mode")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, err := p.pipeReader.Read(buf)
			if err != nil {
				if err == syscall.EAGAIN {
					// 没有数据可读，短暂休眠后继续
					time.Sleep(time.Millisecond * 50)
					continue
				}
				p.L.Error("Pipe read error: %v", err)
				return err
			}
			if n > 0 {
				// 将读取到的数据按行分割
				lines := strings.Split(string(buf[:n]), "\n")
				for _, line := range lines {
					if line == "" {
						continue
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						p.broadcast(line)
					}
				}
			}
		}
	}
}

func (p *Pipe) Subscribe(bufSize int) *Subscriber {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch := make(chan string, bufSize)
	done := make(chan struct{})

	sub := &Subscriber{ch: ch, done: done}
	p.subscribers = append(p.subscribers, sub)
	p.L.Debugf("New subscriber added, current subscribers count: %d", len(p.subscribers))

	sub.doneFunc = func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		close(done)
		// 从订阅者列表中移除
		for i, s := range p.subscribers {
			if s.ch == ch {
				p.subscribers = append(p.subscribers[:i], p.subscribers[i+1:]...)
				break
			}
		}
	}

	return sub
}

// 修改 Serve 方法中的广播逻辑
func (p *Pipe) broadcast(line string) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	p.L.Debugf("Broadcasting message to %d subscribers", len(p.subscribers))
	droppedCount := 0

	for _, sub := range p.subscribers {
		select {
		case <-sub.done:
			continue
		case sub.ch <- line:
			// 成功发送
		default:
			// 如果channel满了，丢弃这条消息
			<-sub.ch
			sub.ch <- line
			droppedCount++
		}
	}

	if droppedCount > 0 {
		p.L.Debugf("Dropped %d messages due to full channels", droppedCount)
	}
}

func (p *Pipe) Close() error {
	p.L.Info("Closing log pipe")
	p.mu.Lock()
	defer p.mu.Unlock()

	subscriberCount := len(p.subscribers)
	for _, sub := range p.subscribers {
		close(sub.done)
		close(sub.ch)
	}
	p.L.Debugf("Closed %d subscriber channels", subscriberCount)

	p.pipeReader.Close()
	p.pipeWriter.Close()

	err := serve.RemoveAndWait(p.token, time.Millisecond*300)
	if err != nil {
		p.L.Errorf("Error while removing service: %v", err)
	}
	return err
}
