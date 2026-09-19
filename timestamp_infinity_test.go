package duckdb

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetTSTicksTimestampInfinity(t *testing.T) {
	positive := time.UnixMicro(math.MaxInt64).UTC()
	negative := time.UnixMicro(math.MinInt64 + 1).UTC()
	finite := time.Date(2026, time.September, 20, 1, 2, 3, 456789000, time.UTC)

	tests := []struct {
		name    string
		typ     Type
		value   time.Time
		want    int64
		wantErr bool
	}{
		{name: "timestamp positive infinity", typ: TYPE_TIMESTAMP, value: positive, want: math.MaxInt64},
		{name: "timestamp negative infinity", typ: TYPE_TIMESTAMP, value: negative, want: math.MinInt64 + 1},
		{name: "timestamptz positive infinity", typ: TYPE_TIMESTAMP_TZ, value: positive, want: math.MaxInt64},
		{name: "timestamptz negative infinity", typ: TYPE_TIMESTAMP_TZ, value: negative, want: math.MinInt64 + 1},
		{name: "same positive instant in another location", typ: TYPE_TIMESTAMP_TZ, value: positive.In(time.FixedZone("offset", 9*60*60)), want: math.MaxInt64},
		{name: "positive sentinel minus one microsecond", typ: TYPE_TIMESTAMP, value: positive.Add(-time.Microsecond), wantErr: true},
		{name: "negative sentinel plus one microsecond", typ: TYPE_TIMESTAMP, value: negative.Add(time.Microsecond), wantErr: true},
		{name: "finite timestamp unchanged", typ: TYPE_TIMESTAMP, value: finite, want: finite.UnixMicro()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getTSTicks(tt.typ, tt.value)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTimestampInfinityParameterRoundTrip(t *testing.T) {
	db := openDbWrapper(t, ``)
	defer closeDbWrapper(t, db)

	tests := []struct {
		name     string
		typeName string
		value    time.Time
		want     string
	}{
		{name: "timestamp positive infinity", typeName: "TIMESTAMP", value: timestampInfinity, want: "infinity"},
		{name: "timestamp negative infinity", typeName: "TIMESTAMP", value: timestampNegInfinity, want: "-infinity"},
		{name: "timestamptz positive infinity", typeName: "TIMESTAMPTZ", value: timestampInfinity, want: "infinity"},
		{name: "timestamptz negative infinity", typeName: "TIMESTAMPTZ", value: timestampNegInfinity, want: "-infinity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			err := db.QueryRow("SELECT CAST(? AS "+tt.typeName+")::VARCHAR", tt.value).Scan(&got)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTimestampInfinityAppenderRoundTrip(t *testing.T) {
	c, db, conn, a := prepareAppender(t, appenderTypeDefault, `CREATE TABLE test (ts TIMESTAMP, tstz TIMESTAMPTZ)`)
	defer cleanupAppender(t, c, db, conn, a)

	require.NoError(t, a.AppendRow(timestampInfinity, timestampInfinity))
	require.NoError(t, a.AppendRow(timestampNegInfinity, timestampNegInfinity))
	require.NoError(t, a.Flush())

	tests := []struct {
		name    string
		literal string
		want    int
	}{
		{name: "positive infinity", literal: "infinity", want: 1},
		{name: "negative infinity", literal: "-infinity", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int
			err := db.QueryRowContext(context.Background(), `
				SELECT count(*) FROM test
				WHERE ts = CAST(? AS TIMESTAMP) AND tstz = CAST(? AS TIMESTAMPTZ)`, tt.literal, tt.literal).Scan(&got)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
