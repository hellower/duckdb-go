package duckdb

// TimestampInfinity is an explicit DuckDB timestamp infinity marker.
// It keeps protocol infinity distinct from finite time.Time values that may
// share the same instant in a wider timestamp precision.
type TimestampInfinity int8

const (
	TimestampNegInfinity TimestampInfinity = -1
	TimestampPosInfinity TimestampInfinity = 1
)
