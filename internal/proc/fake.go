package proc

import "context"

type Call struct {
	Spec Spec
}

type Fake struct {
	Calls   []Call
	Handler func(s Spec) error
}

func (f *Fake) Run(_ context.Context, s Spec) error {
	f.Calls = append(f.Calls, Call{Spec: s})
	if f.Handler != nil {
		return f.Handler(s)
	}
	return nil
}
