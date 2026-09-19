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
		value   any
		want    int64
		wantErr bool
	}{
		{name: "timestamp scanned positive infinity", typ: TYPE_TIMESTAMP, value: positive, want: math.MaxInt64},
		{name: "timestamp scanned negative infinity", typ: TYPE_TIMESTAMP, value: negative, want: math.MinInt64 + 1},
		{name: "timestamptz scanned positive infinity", typ: TYPE_TIMESTAMP_TZ, value: positive, want: math.MaxInt64},
		{name: "timestamptz scanned negative infinity", typ: TYPE_TIMESTAMP_TZ, value: negative, want: math.MinInt64 + 1},
		{name: "timestamp positive marker", typ: TYPE_TIMESTAMP, value: TimestampPosInfinity, want: math.MaxInt64},
		{name: "timestamp negative marker", typ: TYPE_TIMESTAMP, value: TimestampNegInfinity, want: math.MinInt64 + 1},
		{name: "timestamptz positive marker", typ: TYPE_TIMESTAMP_TZ, value: TimestampPosInfinity, want: math.MaxInt64},
		{name: "timestamptz negative marker", typ: TYPE_TIMESTAMP_TZ, value: TimestampNegInfinity, want: math.MinInt64 + 1},
		{name: "timestamp_s marker rejected", typ: TYPE_TIMESTAMP_S, value: TimestampPosInfinity, wantErr: true},
		{name: "timestamp_ms marker rejected", typ: TYPE_TIMESTAMP_MS, value: TimestampPosInfinity, wantErr: true},
		{name: "timestamp_ns marker rejected", typ: TYPE_TIMESTAMP_NS, value: TimestampPosInfinity, wantErr: true},
		{name: "timestamp_s same instant remains finite", typ: TYPE_TIMESTAMP_S, value: positive, want: positive.Unix()},
		{name: "timestamp_ms same instant remains finite", typ: TYPE_TIMESTAMP_MS, value: positive, want: positive.UnixMilli()},
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
	tests := []struct {
		name     string
		typeName string
		value    any
		want     string
	}{
		{name: "timestamp positive marker", typeName: "TIMESTAMP", value: TimestampPosInfinity, want: "infinity"},
		{name: "timestamp negative marker", typeName: "TIMESTAMP", value: TimestampNegInfinity, want: "-infinity"},
		{name: "timestamptz positive marker", typeName: "TIMESTAMPTZ", value: TimestampPosInfinity, want: "infinity"},
		{name: "timestamptz negative marker", typeName: "TIMESTAMPTZ", value: TimestampNegInfinity, want: "-infinity"},
		{name: "timestamp scanned positive infinity", typeName: "TIMESTAMP", value: timestampInfinity, want: "infinity"},
		{name: "timestamp scanned negative infinity", typeName: "TIMESTAMP", value: timestampNegInfinity, want: "-infinity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openDbWrapper(t, ``)
			defer closeDbWrapper(t, db)
			_, err := db.Exec("CREATE TABLE test (v " + tt.typeName + ")")
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO test VALUES (?)", tt.value)
			require.NoError(t, err)
			var got string
			require.NoError(t, db.QueryRow("SELECT v::VARCHAR FROM test").Scan(&got))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTimestampInfinityParameterRejectsWiderPrecisions(t *testing.T) {
	for _, typeName := range []string{"TIMESTAMP_S", "TIMESTAMP_MS", "TIMESTAMP_NS"} {
		t.Run(typeName, func(t *testing.T) {
			db := openDbWrapper(t, ``)
			defer closeDbWrapper(t, db)
			_, err := db.Exec("CREATE TABLE test (v " + typeName + ")")
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO test VALUES (?)", TimestampPosInfinity)
			require.Error(t, err)
		})
	}
}

func TestTimestampInfinityAppenderRoundTrip(t *testing.T) {
	c, db, conn, a := prepareAppender(t, appenderTypeDefault, `CREATE TABLE test (ts TIMESTAMP, tstz TIMESTAMPTZ)`)
	defer cleanupAppender(t, c, db, conn, a)

	require.NoError(t, a.AppendRow(TimestampPosInfinity, TimestampPosInfinity))
	require.NoError(t, a.AppendRow(TimestampNegInfinity, TimestampNegInfinity))
	require.NoError(t, a.AppendRow(timestampInfinity, timestampInfinity))
	require.NoError(t, a.AppendRow(timestampNegInfinity, timestampNegInfinity))
	require.NoError(t, a.Flush())

	tests := []struct {
		name    string
		literal string
		want    int
	}{
		{name: "positive infinity", literal: "infinity", want: 2},
		{name: "negative infinity", literal: "-infinity", want: 2},
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
