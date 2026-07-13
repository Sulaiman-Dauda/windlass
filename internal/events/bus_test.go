package events

import (
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Publish(Event{Topic: "deployment", Type: "deployment.started", Resource: "crm"})

	select {
	case e := <-ch:
		if e.Type != "deployment.started" || e.Resource != "crm" {
			t.Errorf("got %+v", e)
		}
		if e.Time.IsZero() {
			t.Error("time not stamped")
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestTopicFilter(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe("system")
	defer cancel()

	b.Publish(Event{Topic: "deployment", Type: "deployment.started"})
	b.Publish(Event{Topic: "system", Type: "system.update"})

	select {
	case e := <-ch:
		if e.Topic != "system" {
			t.Errorf("got filtered-out topic %q", e.Topic)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	b := NewBus()
	_, cancel := b.Subscribe() // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(Event{Topic: "t", Type: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked on slow subscriber")
	}
}

func TestCancelClosesChannel(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe()
	cancel()
	if _, ok := <-ch; ok {
		t.Error("channel not closed after cancel")
	}
	// Publishing after cancel must not panic.
	b.Publish(Event{Topic: "t", Type: "x"})
}
