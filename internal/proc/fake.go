package proc

import "context"

type Call struct {
	Spec   Spec
	CtxErr error
}

type Fake struct {
	Calls   []Call
	Handler func(s Spec) error
}

func (f *Fake) Run(ctx context.Context, s Spec) error {
	f.Calls = append(f.Calls, Call{Spec: s, CtxErr: ctx.Err()})
	if f.Handler != nil {
		return f.Handler(s)
	}
	return nil
}
