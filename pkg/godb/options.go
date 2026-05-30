package godb

// options is the unexported config struct that Open uses internally.
// Option values mutate it via the functional options pattern.
type options struct {
	createIfMissing bool
}

// defaultOptions returns the defaults a fresh Open uses.
func defaultOptions() options {
	return options{createIfMissing: true}
}

// Option configures the database at Open time. Callers compose
// options with godb.Open(path, godb.WithXxx(...), ...).
type Option func(*options)

// WithCreateIfMissing controls whether Open creates the database
// file if it doesn't already exist. Default is true. Set to false
// to require the file to exist; Open then returns an error wrapping
// os.ErrNotExist if it doesn't.
func WithCreateIfMissing(b bool) Option {
	return func(o *options) { o.createIfMissing = b }
}
