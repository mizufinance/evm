package types

import (
	"errors"
	"reflect"
	"testing"
)

type adoptionScope struct {
	events *[]string
	fail   bool
}

func (s adoptionScope) PrepareAdopt() error {
	*s.events = append(*s.events, "prepare")
	if s.fail {
		return errors.New("uncertain close")
	}
	return nil
}
func (s adoptionScope) Adopt() { *s.events = append(*s.events, "adopt") }
func (s adoptionScope) Abort() {}
func TestAdoptionClosesBeforeWriting(t *testing.T) {
	var events []string
	AdoptExecutionScope(adoptionScope{events: &events}, func() { events = append(events, "write") })
	if !reflect.DeepEqual(events, []string{"prepare", "write", "adopt"}) {
		t.Fatal(events)
	}
}
func TestAdoptionFailureIsFatalAndDoesNotWrite(t *testing.T) {
	var events []string
	defer func() {
		if _, ok := recover().(ExecutionScopePanic); !ok {
			t.Fatal("close failure was not fatal")
		}
		if !reflect.DeepEqual(events, []string{"prepare"}) {
			t.Fatal(events)
		}
	}()
	AdoptExecutionScope(adoptionScope{events: &events, fail: true}, func() { events = append(events, "write") })
}
