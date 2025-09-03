// Package ppp provides a basic interface for parsing, processing, and presenting data.
package ppp

import (
	"context"
	"io"
)

// Parser is an interface for parsing input data.
type Parser[I any] interface {
	Parse(io.Reader) (I, error)
}

// Presenter is an interface for presenting output data.
type Presenter[O any] interface {
	Present(io.Writer, O) error
}

// Processor is an interface for processing input data into output data.
type Processor[I, O any] interface {
	Process(context.Context, I) (O, error)
}

// Executor is a struct that encapsulates the parsing, processing, and presenting of data.
type Executor[I, O any] struct {
	parser    Parser[I]
	processor Processor[I, O]
	presenter Presenter[O]
}

// NewExecutor creates a new Executor with the given parser, processor, and presenter.
func NewExecutor[I, O any](parser Parser[I], processor Processor[I, O], presenter Presenter[O]) *Executor[I, O] {
	return &Executor[I, O]{
		parser:    parser,
		processor: processor,
		presenter: presenter,
	}
}

// Execute runs the parsing, processing, and presenting steps in order.
func (e *Executor[I, O]) Execute(ctx context.Context, r io.Reader, w io.Writer) error {
	input, err := e.parser.Parse(r)
	if err != nil {
		return err
	}

	output, err := e.processor.Process(ctx, input)
	if err != nil {
		return err
	}

	return e.presenter.Present(w, output)
}
