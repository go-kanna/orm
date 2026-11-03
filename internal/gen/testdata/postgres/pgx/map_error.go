package pgx

// MappedError is an error OrderRepository returns, which has gone through its mapError.
type MappedError struct{ Err error }

func (e MappedError) Error() string { return e.Err.Error() }

func (e MappedError) Unwrap() error { return e.Err }

func (OrderRepository) mapError(err error) error { return MappedError{err} }
