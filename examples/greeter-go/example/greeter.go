// Package example is a minimal sample module: one interface and one
// implementation behind it, so lint, tests, and the docs/diagram pipeline all
// have real input immediately after setup.
package example

// Greeter produces a greeting for a name. It is the module's boundary — the
// contract other code depends on rather than the concrete type.
type Greeter interface {
	Greet(name string) string
}

// EnglishGreeter greets in English.
type EnglishGreeter struct{}

var _ Greeter = EnglishGreeter{}

// Greet returns an English greeting, defaulting an empty name to "World".
func (EnglishGreeter) Greet(name string) string {
	if name == "" {
		name = "World"
	}
	return "Hello, " + name + "!"
}
