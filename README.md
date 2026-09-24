# Go SQL Driver For [DuckDB](https://github.com/duckdb/duckdb)

> [!IMPORTANT]
> **이 저장소는 [`duckdb/duckdb-go`](https://github.com/duckdb/duckdb-go)의 일반 배포용 미러가 아니라, GooseDB가 필요한 미출시 수정만 고정해서 사용하는 임시 fork입니다.**
>
> GooseDB의 현재 기준 버전은 **`v2.10505.1-panicfix.7`**이며, 비교 기준은 업스트림의 **`v2.10505.0`**입니다. `main` 브랜치는 업스트림 동기화 브랜치가 아니므로 의존성으로 사용하지 마십시오. 반드시 태그를 고정하고, Go 소스의 import 경로는 계속 `github.com/duckdb/duckdb-go/v2`를 사용해야 합니다.
>
> **업스트림 제출 PR: [`duckdb/duckdb-go#182`](https://github.com/duckdb/duckdb-go/pull/182) — `fix: stop the interrupter goroutine when the wrapped call panics`(open), [`duckdb/duckdb-go#184`](https://github.com/duckdb/duckdb-go/pull/184) — `Preserve logical type aliases in TypeInfo`(2026-09-22 merge, 정식 태그 미포함)** (2026-09-25 확인 기준)
>
> **업스트림 이슈: [`duckdb/duckdb-go#185`](https://github.com/duckdb/duckdb-go/issues/185) — 닫지 않은 준비문이 `DB.Close` 뒤에도 DB를 열어 둔다**(open, 수정 4의 원인 보고. PR은 아직 제출하지 않음)

## GooseDB fork 안내

### 한눈에 보는 차이

이 fork의 권장 태그 `v2.10505.1-panicfix.7`는 업스트림 `v2.10505.0`에 아래 네 동작 수정만 누적한 판본입니다.

| 구분 | 업스트림 `v2.10505.0` | 이 fork `v2.10505.1-panicfix.7` |
|---|---|---|
| 기반 DuckDB | `v1.5.5` | 동일 |
| Go bindings | `v0.10505.0` | 동일 |
| 모듈/import 경로 | `github.com/duckdb/duckdb-go/v2` | 동일 |
| panic 이후 context interrupter | 정리 코드에 도달하지 못하면 goroutine이 남을 수 있음 | `defer`에서 종료 신호를 보내고 goroutine 종료까지 대기 |
| `TIMESTAMP`/`TIMESTAMPTZ` infinity 재입력 | 조회된 infinity `time.Time`을 다시 parameter/Appender로 쓰면 유한 연도 범위 검사에서 거부될 수 있음 | 조회값 왕복과 명시적 `TimestampPosInfinity`/`TimestampNegInfinity` 입력을 보존 |
| `TIMESTAMP_S`/`TIMESTAMP_MS`/`TIMESTAMP_NS` | 별도 명시적 infinity marker 없음 | marker를 의도적으로 거부하여 같은 `time.Time` instant를 유한값으로 유지 |
| 논리 타입 alias(`Stmt.ColumnTypeInfo`) | alias를 버려 JSON 결과 컬럼과 일반 `VARCHAR`가 구별되지 않음 | `TypeInfo.Alias()`로 노출(LIST·ARRAY·STRUCT·MAP·UNION 안쪽 포함) |
| alias가 있는 `TypeInfo`를 DuckDB에 다시 넘길 때 | alias가 사라져 일반 `VARCHAR`로 동작 | alias를 되살림. JSON이면 쓰는 값을 `encoding/json`으로 marshal |
| 닫지 않은 준비문이 남은 채 `Conn.Close` | `Disconnect`만 하여 준비문이 DB 인스턴스를 붙잡음. `DB.Close`가 nil을 돌려준 뒤에도 워커 스레드와 **DB 파일 잠금**이 남음 | `Disconnect` 전에 남은 준비문을 파괴. 이후 그 준비문 사용은 닫힌 문장 오류, 다시 닫기는 no-op |

전체 순변경은 [태그 비교](https://github.com/hellower/duckdb-go/compare/v2.10505.0...v2.10505.1-panicfix.7) 기준 **9 commits, 13 files, +926/-29 lines**입니다. DuckDB 엔진, bindings, `go.mod`, `go.sum`은 바꾸지 않았습니다.

### 수정 1: panic 뒤에 남는 interrupter goroutine 종료

업스트림 `runWithCtxInterrupt`는 감싼 함수가 정상 반환한 뒤에만 `done`을 닫고 interrupter goroutine을 기다렸습니다. 감싼 함수가 panic하면 이 정리 구간을 건너뛰므로, 호출자가 panic을 복구한 뒤 같은 연결을 계속 사용할 때 남은 goroutine이 다음 질의를 interrupt할 수 있습니다. 연결이 닫힌 뒤에도 해제된 native handle에 `duckdb_interrupt`를 호출하면 프로세스 crash로 이어질 수 있습니다.

fork는 정리 동작을 `defer`로 옮겨 다음 계약을 보장합니다.

- 감싼 함수의 panic은 원래대로 호출자에게 전파합니다.
- panic 전후와 context cancel 순서에 관계없이 `done`을 닫습니다.
- interrupter goroutine이 실제로 끝날 때까지 기다린 뒤 연결 사용권을 반환합니다.
- 회귀 테스트는 cancel 전 panic과 cancel 후 panic을 각각 검증합니다.

이 수정의 업스트림 제안은 [`duckdb/duckdb-go#182`](https://github.com/duckdb/duckdb-go/pull/182)입니다. 2026-09-25 확인 기준 아직 open 상태이므로, 업스트림의 정식 안정 태그에는 포함되지 않았습니다.

### 수정 2: timestamp infinity의 손실 없는 쓰기/왕복

DuckDB의 `TIMESTAMP`와 `TIMESTAMPTZ` infinity를 Go로 읽으면 드라이버는 극값에 해당하는 `time.Time`을 반환합니다. 업스트림 `getTSTicks`는 그 값을 다시 쓸 때 먼저 유한 timestamp 연도 범위를 검사하므로, 다음과 같은 read → write 왕복이 실패할 수 있습니다.

- `database/sql` parameter binding
- DuckDB Appender
- PostgreSQL 호환 프로토콜처럼 infinity를 별도 sentinel로 전달하는 상위 계층

fork는 두 입력 경로를 구분합니다.

1. `TIMESTAMP`/`TIMESTAMPTZ`에서 실제로 scan된 정확한 양·음 infinity `time.Time`은 DuckDB sentinel tick으로 되돌립니다.
2. 상위 프로토콜이 유한 시각과 infinity를 모호하지 않게 구분할 수 있도록 공개 타입 `TimestampInfinity`와 상수 `TimestampPosInfinity`, `TimestampNegInfinity`를 제공합니다.

명시적 marker는 `TIMESTAMP`와 `TIMESTAMPTZ`에만 허용합니다. 초·밀리초·나노초 정밀도 타입에서는 동일한 Go instant가 정상적인 유한값일 수 있으므로, `TIMESTAMP_S`, `TIMESTAMP_MS`, `TIMESTAMP_NS`에 marker를 적용하면 오류를 반환합니다. 이 제한은 유한값을 infinity로 잘못 바꾸는 충돌을 막기 위한 계약입니다.

예시:

```go
_, err := db.Exec(
	"INSERT INTO events (occurred_at) VALUES (?)",
	duckdb.TimestampPosInfinity,
)
```

Appender에도 같은 marker를 전달할 수 있습니다.

```go
err := appender.AppendRow(duckdb.TimestampNegInfinity)
```

### 수정 3: 논리 타입 alias 보존

DuckDB는 `JSON`을 `VARCHAR` 저장 위에 alias를 붙인 논리 타입으로 표현합니다. prepared statement 결과 메타데이터는 C API로 그 alias를 이미 제공하지만, 업스트림 `newTypeInfoFromLogicalType`은 `TypeInfo`를 만들면서 alias를 버립니다. 그래서 `Stmt.ColumnTypeInfo`만으로는 JSON 결과 컬럼과 일반 `VARCHAR` 결과 컬럼을 구별할 수 없습니다.

fork는 다음 계약을 보장합니다.

- `TypeInfo` 인터페이스에 `Alias()`를 추가합니다. alias가 없으면 빈 문자열을 반환합니다.
- `Stmt.ColumnTypeInfo`가 LIST·ARRAY·STRUCT·MAP·UNION 안쪽의 alias까지 보존합니다.
- `TypeInfo`를 다시 DuckDB 논리 타입으로 만들 때 alias를 되살립니다.
- 지원하지 않는 자식 타입(예: `BIGNUM`) 때문에 중첩 논리 타입 생성이 NULL을 돌려주면 alias 재적용을 건너뜁니다. `duckdb_logical_type_set_alias`는 NULL을 검사하지 않으므로, 이 가드가 없으면 프로세스가 SIGSEGV로 죽습니다.

alias는 DuckDB에서 얻은 `TypeInfo`에만 붙습니다. `New*Info`로 만든 `TypeInfo`의 `Alias()`는 항상 빈 문자열입니다. 실제로 보이는 값은 `JSON`이나 `INET` 같은 확장 타입 이름이며, `CREATE TYPE`으로 만든 사용자 타입 이름은 보고되지 않습니다.

**동작 변경:** DuckDB에서 얻은 JSON `TypeInfo`를 `NewQueryAppender`나 UDF 타입으로 다시 넘기면, 이제 일반 `VARCHAR`가 아니라 JSON으로 동작합니다.

- Appender 행과 scalar UDF 결과는 `encoding/json`으로 marshal됩니다. Go 문자열 `{"a":1}`은 객체가 아니라 JSON 문자열 `"{\"a\":1}"`로 저장됩니다. JSON 문서를 그대로 쓰려면 `json.RawMessage`나 map·struct를 넘기십시오. `[]byte`는 base64 JSON 문자열이 됩니다.
- scalar UDF 입력은 `encoding/json`으로 unmarshal됩니다. 예를 들어 JSON 객체는 원문 텍스트가 아니라 `map[string]any`로 들어옵니다.

> [!WARNING]
> `v2.10505.0` 기반의 한계: `SetChunkValue`와 `SetRowValue`(`Row.SetRowValue` 포함, row 기반 table UDF가 쓰는 경로)는 JSON alias를 무시하고 문자열을 그대로 쓰며, `VARCHAR`·JSON 열에 `nil`을 쓰면 panic합니다. `DataChunk.SetValue`는 Appender와 같은 경로라 JSON alias를 따릅니다. 이 fork 이전부터 있던 `v2.10505.0`의 동작이고, 업스트림은 이후 [`6bee668`](https://github.com/duckdb/duckdb-go/commit/6bee668f2e3b21249e54443d89e09b7502915181)(쓰기 디스패치 단일화)에서 고쳤습니다. 그래서 이 fork에는 업스트림 PR의 table UDF JSON 테스트를 싣지 않았습니다.

이 수정의 업스트림 제안은 [`duckdb/duckdb-go#184`](https://github.com/duckdb/duckdb-go/pull/184)입니다. 2026-09-22에 업스트림 `main`에 merge됐지만, 2026-09-25 확인 기준 이를 포함한 정식 태그는 아직 없습니다.

### 수정 4: `Conn.Close`가 남은 준비문을 파괴

`database/sql`은 `sql.Conn.PrepareContext`처럼 한 연결에서 만든 준비문을 추적하지 않습니다(`driverConn.prepareLocked`: "No need to manage open statements if there is a single connection grabber."). 대신 `driver.Conn.Close` 계약("Close invalidates and potentially stops any current prepared statements and transactions")에 기댑니다. 업스트림 `Conn.Close`는 `duckdb_disconnect`만 호출하므로, 닫지 않은 준비문의 native 핸들이 `ClientContext`를 거쳐 `DatabaseInstance`를 계속 붙잡습니다. 그 결과 `DB.Close()`가 nil을 돌려준 뒤에도 인스턴스가 살아 있어 워커 스레드와 DB 파일 잠금이 남고, 다른 프로세스는 `Could not set lock on file … Conflicting lock is held`로 같은 파일을 열지 못합니다(업스트림 `v2.10505.0`에서 독립 재현, [`duckdb/duckdb-go#185`](https://github.com/duckdb/duckdb-go/issues/185)).

fork는 다음 계약을 보장합니다.

- `Conn`은 자기가 준비한 문장을 기록하고, `Stmt.Close`는 그 기록에서 지웁니다.
- `Conn.Close`는 `Disconnect` **전에** 아직 열린 준비문을 모두 파괴합니다.
- `Stmt.Close`와 `Conn.Close`가 겹쳐도 같은 준비문을 두 번 파괴하지 않도록 mutex로 직렬화합니다.
- `Conn.Close`가 파괴한 준비문을 나중에 쓰면 panic이나 해제된 핸들 접근 없이 닫힌 문장 오류를 돌려줍니다. `NumInput`은 -1(개수 모름)을, `Close`는 몇 번을 불러도 nil을 돌려줍니다.
- 호출자가 직접 닫은 준비문을 다시 닫으면 업스트림과 같이 misuse panic입니다(동작 불변).

이 수정은 업스트림 이슈 [`duckdb/duckdb-go#185`](https://github.com/duckdb/duckdb-go/issues/185)의 원인에 대한 것이며, fork 브랜치 `fix/conn-close-stmts-10505`에 업스트림에 그대로 낼 수 있는 형태로 있습니다. 업스트림 PR은 아직 제출하지 않았습니다.

### 태그 계보

모든 `panicfix` 태그는 업스트림 `v2.10505.0`에서 갈라진 누적 태그입니다.

| 태그 | 추가된 내용 | 사용 권장 여부 |
|---|---|---|
| `v2.10505.1-panicfix.1` | interrupter panic cleanup | timestamp infinity 수정이 필요 없는 기존 사용자만 |
| `v2.10505.1-panicfix.2` | microsecond timestamp infinity 쓰기 보존의 최초 구현 | 중간 태그 |
| `v2.10505.1-panicfix.3` | 모든 timestamp 정밀도로 판정 범위를 넓힌 중간 설계 | 중간 태그 |
| `v2.10505.1-panicfix.4` | 정밀도별 native sentinel 왕복을 추가한 중간 설계 | 중간 태그 |
| `v2.10505.1-panicfix.5` | 명시적 protocol marker를 도입하고 유한값과의 충돌을 제거한 timestamp 계약 | alias 수정이 필요 없는 기존 사용자만 |
| `v2.10505.1-panicfix.6` | 논리 타입 alias 보존(`TypeInfo.Alias()`)과 중첩 NULL 논리 타입 가드 | `Conn.Close` 수정이 필요 없는 기존 사용자만 |
| `v2.10505.1-panicfix.7` | `Conn.Close`가 남은 준비문을 파괴(DB 인스턴스·파일 잠금 해제) | **권장** |

새 소비자는 중간 태그를 순서대로 적용할 필요가 없습니다. 최종 누적 태그인 `v2.10505.1-panicfix.7`만 고정하십시오.

`panicfix.5`와 `panicfix.6` 사이의 `f8eb8fb`(태그 없음)는 alias의 최초 구현으로, 위 NULL 가드가 없습니다. 이 commit을 pseudo-version으로 고정하지 마십시오.

### Go 모듈에 적용하는 방법

fork도 원래 module path를 유지하므로 애플리케이션의 import 문은 바꾸지 않습니다.

```go
import duckdb "github.com/duckdb/duckdb-go/v2"
```

`go.mod`에서 업스트림 요구사항을 fork 태그로 치환합니다.

```mod
require github.com/duckdb/duckdb-go/v2 v2.10505.0

replace github.com/duckdb/duckdb-go/v2 v2.10505.0 => github.com/hellower/duckdb-go/v2 v2.10505.1-panicfix.7
```

그다음 `go mod tidy`를 실행하고 `go.sum`에 `github.com/hellower/duckdb-go/v2 v2.10505.1-panicfix.7`이 기록됐는지 확인합니다. `main`, branch 이름, commit pseudo-version 대신 위 태그를 사용해야 재현 가능한 빌드가 됩니다.

### 검증 범위

fork가 추가한 회귀 테스트는 다음을 직접 확인합니다.

- panic이 cancel 전/후에 발생해도 interrupter가 남지 않는지
- scan으로 얻은 양·음 infinity를 parameter와 Appender로 왕복할 수 있는지
- 명시적 양·음 infinity marker가 `TIMESTAMP`와 `TIMESTAMPTZ`에서 보존되는지
- 인접한 유한 경계값이 infinity로 오인되지 않는지
- 더 넓은 정밀도 타입에서 marker가 명시적으로 거부되는지
- `Stmt.ColumnTypeInfo`가 스칼라와 중첩(LIST·ARRAY·STRUCT·MAP·UNION) JSON alias, 그리고 빈 alias를 보고하고, `TypeInfo → 논리 타입 → TypeInfo` 왕복에서 유지하는지
- JSON `ColumnTypeInfo`로 만든 query appender가 JSON 열의 table appender와 같은 값을 저장하는지
- scalar UDF의 JSON 입력이 unmarshal되고 결과가 marshal되는지
- 지원하지 않는 자식을 가진 alias 중첩 타입이 NULL 논리 타입을 DuckDB에 넘기지 않는지
- 준비문을 남긴 채 `Conn`·`DB`를 닫으면 **다른 프로세스가 같은 DB 파일을 열 수 있고** 워커 스레드가 사라지는지(대조군: 준비문을 먼저 닫은 경우)
- `Conn.Close` 뒤 그 준비문의 실행·바인드·메타데이터 호출이 닫힌 문장 오류를 돌려주고, `Close`를 두 번 불러도 안전한지
- 결과 행이 열린 채 연결을 닫아도 결과를 읽고 닫을 수 있는지, 그리고 `Stmt.Close`와 `Conn.Close`를 동시에 불러도 이중 파괴가 없는지(`-race`)

태그 자체를 확인하려면 다음 표적 테스트를 실행합니다.

```sh
git checkout v2.10505.1-panicfix.7
go test -count=1 -run '^(TestRunWithCtxInterrupt_|TestGetTSTicksTimestampInfinity|TestTimestampInfinity|TestPreparedStatementColumnTypeInfoAliases|TestTypeInfoAliasWithUnsupportedChild|TestQueryAppenderColumnTypeInfoJSON|TestJSONScalarUDF|TestConnClose)' ./...
go test -race -count=1 -run '^TestConnClose' .
```

### fork 제거 조건

이 fork는 영구적인 독자 배포판을 목표로 하지 않습니다. 아래 조건을 모두 만족하면 `replace`를 제거하고 업스트림 정식 태그로 복귀합니다.

1. 네 수정과 동등한 구현이 업스트림에 merge됩니다.
2. 그 구현이 포함된 안정 태그가 발행됩니다.
3. GooseDB의 parameter binding, Appender, COPY BINARY timestamp infinity 회귀 테스트, JSON 결과 컬럼(alias) 판별 테스트, 준비문을 남긴 채 닫은 DB의 파일 잠금 해제 테스트가 그 태그에서 통과합니다.
4. `go.mod`의 `replace` 제거와 함께 정적 DuckDB bindings/번들 좌표의 호환성을 다시 검증합니다.

위 조건 전에는 “업스트림 `main`에 코드가 보인다”는 사실만으로 이 fork를 제거하지 않습니다. 소비자는 commit이 아니라 재현 가능한 정식 태그와 전체 런타임 검증을 기준으로 전환해야 합니다.

![Tests status](https://github.com/duckdb/duckdb-go/actions/workflows/tests.yaml/badge.svg)
[![GoDoc](https://godoc.org/github.com/duckdb/duckdb-go/v2?status.svg)](https://pkg.go.dev/github.com/duckdb/duckdb-go/v2)

The DuckDB driver conforms to the built-in `database/sql` interface.

**Current DuckDB version: `v1.5.0`.**

The first duckdb-go tag with that version is `v2.10500.0`.

Starting with DuckDB `v1.5.0`, the duckdb-go version encodes the DuckDB version in its second semver component.
The format is `v2.MAJOR_MINOR_PATCH.x`, e.g., DuckDB `v1.5.0` maps to duckdb-go `v2.10500.x`.

Previous DuckDB versions:

| DuckDB   | duckdb-go    |
| -------- | ------------ |
| `v1.5.0` | `v2.10500.0` |
| `v1.4.4` | `v2.5.5`     |
| `v1.4.3` | `v2.5.4`     |
| `v1.4.2` | `v2.5.2`     |
| `v1.4.1` | `v2.4.2`     |
| `v1.4.0` | `v2.4.0`     |
| `v1.3.2` | `v2.3.3`     |
| `v1.3.1` | `v2.3.2`     |
| `v1.3.0` | `v2.3.0`     |
| `v1.2.2` | `v2.2.0`     |
| `v1.2.1` | `v2.1.0`     |
| `v1.2.0` | `v2.0.3`     |
| `v1.1.3` | `v1.8.5`     |

## Migration from marcboeker/go-duckdb

**This project moved from `github.com/marcboeker/go-duckdb` to `github.com/duckdb/duckdb-go` starting with `v2.5.0`.**

All versions prior to `v2.5.0` use the old import paths.

To migrate:

```sh
# Update dependency
go get github.com/duckdb/duckdb-go/v2@v2.5.0

# Rewrite import paths
gofmt -w -r '"github.com/marcboeker/go-duckdb/v2" -> "github.com/duckdb/duckdb-go/v2"' .

# If you use the mapping or arrowmapping submodules, also run
gofmt -w -r '"github.com/marcboeker/go-duckdb/mapping" -> "github.com/duckdb/duckdb-go/v2/mapping"' .
gofmt -w -r '"github.com/marcboeker/go-duckdb/arrowmapping" -> "github.com/duckdb/duckdb-go/v2/arrowmapping"' .

# Clean up
go mod tidy
```

### Background

Over the last few years, the Go client has become a [primary DuckDB client](https://duckdb.org/docs/stable/clients/overview).
We'd like to thank [Marc Boeker](https://github.com/marcboeker) for all his work on creating this driver and implementing the various interfaces of the `database/sql` package!
We'd also like to thank all the other external contributors for their various PRs and other contributions.

With the driver being a primary DuckDB client, over the last years, the DuckDB team has gradually increased its involvement in the maintenance of the driver,
to guarantee that it is constantly updated and that critical bugs (e.g., crashes) are fixed.
Now we have a Long-Term Support release, so starting from early next year, we will have two releases in parallel (v1.4 LTS and v1.5).
Additionally, more DuckDB customers use and rely on the Go client, which necessitates prioritization of certain features from us.

These points all add to the maintenance work, which the DuckDB team is happy to perform!
However, the motivation behind this fork, which is a joint effort of Marc Boeker and the DuckDB team,
is to fully transfer the maintenance and day-to-day work of the driver to the DuckDB team.
That being said, the DuckDB Go client has become what it is also due to its many contributions from the community,
and we are looking forward to your future PRs, issues, and discussions!

The license is unchanged: the migrated repository keeps the original MIT license, which is the same for core DuckDB and other primary clients.

## Breaking Changes

> [!WARNING]
> Starting with `v2.0.0`, duckdb-go supports DuckDB `v1.2.0` and upward.
> Moving to `v2` includes the following list of breaking changes.

#### Dropping pre-built FreeBSD support

Starting with `v2`, duckdb-go drops pre-built FreeBSD support.
This change is because DuckDB does not publish any bundled FreeBSD libraries.
Thus, you must build your static library for FreeBSD using the steps below.

#### The Arrow dependency is now opt-in

The [DuckDB Arrow Interface](https://duckdb.org/docs/api/c/api#arrow-interface) is a heavy dependency.
Starting with `v2`, the DuckDB Arrow Interface is opt-in instead of opt-out.
If you want to use it, you can enable it by passing `-tags=duckdb_arrow` to `go build`.

#### JSON type scanning changes

The pre-built libraries ship DuckDB's JSON extension containing the `JSON` type.
Pre-v2, it was possible to scan a JSON type into `[]byte` via [`Rows.Scan`](https://pkg.go.dev/database/sql#Rows.Scan).
However, scanning into `any` (`driver.Value`) would cause the JSON string to contain escape characters and other unexpected behavior.

It is now possible to scan into `any`, or directly into duckdb-go's `Composite` type,
as shown in the [JSON example](https://github.com/duckdb/duckdb-go/blob/main/examples/json/main.go).
Scanning directly into `string` or `[]byte` is no longer possible.
A workaround is casting to `::VARCHAR` or `::BLOB` in DuckDB if you do not need to scan the result into a JSON interface.

## Installation

```sh
go get github.com/duckdb/duckdb-go/v2
```

### Windows

You must have the correct version of gcc and the necessary runtime libraries installed on Windows.
One method to do this is using msys64.
To begin, install msys64 using their installer.
Once you installed msys64, open a msys64 shell and run:

```sh
pacman -S mingw-w64-ucrt-x86_64-gcc
```

Select "yes" when necessary; it is okay if the shell closes.
Then, add gcc to the path using whatever method you prefer.
In powershell this is `$env:PATH = "C:\msys64\ucrt64\bin;$env:PATH"`.
After, you can compile this package in Windows.

### Vendoring

You can use `go mod vendor` to make a copy of the third-party packages in this package, including the pre-built DuckDB libraries in [duckdb-go-bindings](https://github.com/duckdb/duckdb-go-bindings).

## Usage

_Note: For readability, we omit error handling in most examples._

`duckdb-go` hooks into the `database/sql` interface provided by the Go `stdlib`.
To open a connection, specify the driver type as `duckdb`.

```go
db, err := sql.Open("duckdb", "")
defer db.Close()
```

The above lines create an in-memory instance of DuckDB.
To open a persistent database, specify a file path to the database file.
If the file does not exist, then DuckDB creates it.

```go
db, err := sql.Open("duckdb", "/path/to/foo.db")
defer db.Close()
```

If you want to set specific [config options for DuckDB](https://duckdb.org/docs/sql/configuration),
you can add them as query style parameters in the form of `name=value` pairs to the DSN.

```go
db, err := sql.Open("duckdb", "/path/to/foo.db?access_mode=read_only&threads=4")
defer db.Close()
```

Alternatively, you can use [sql.OpenDB](https://cs.opensource.google/go/go/+/refs/tags/go1.23.0:src/database/sql/sql.go;l=824).
That way, you can perform initialization steps in a callback function before opening the database.
Here's an example that configures some parameters when opening a database with `sql.OpenDB(connector)`.

```go
c, err := duckdb.NewConnector("/path/to/foo.db?access_mode=read_only&threads=4", func(execer driver.ExecerContext) error {
    bootQueries := []string{
        `SET schema=main`,
        `SET search_path=main`,
    }
    for _, query := range bootQueries {
        _, err = execer.ExecContext(context.Background(), query, nil)
        if err != nil {
            return err
        }
    }
    return nil
})
defer c.Close()
db := sql.OpenDB(c)
defer db.Close()
```

Please refer to the [database/sql](https://godoc.org/database/sql) documentation for further instructions on usage.

## Linking DuckDB

By default, `duckdb-go` statically links pre-built DuckDB libraries into your binary.
Statically linking DuckDB increases your binary size.

`duckdb-go` bundles the following pre-compiled static libraries.

- MacOS: amd64, arm64.
- Linux: amd64, arm64.
- Windows: amd64.

### Linking a Static Library

If none of the pre-built libraries satisfy your needs, you can build a custom static library.

1. Clone and build the DuckDB source code.
   - Use their `bundle-library` Makefile target (e.g., `make bundle-library`).
   - Common build flags are: `DUCKDB_PLATFORM=any BUILD_EXTENSIONS="icu;json;parquet;autocomplete"`.
   - See DuckDB's [development](https://github.com/duckdb/duckdb#development) instructions for more details.
2. Link against the resulting static library, which you can find in: `duckdb/build/release/libduckdb_bundle.a`.

For Darwin ARM64, you can then build your module like so:

```sh
CGO_ENABLED=1 CPPFLAGS="-DDUCKDB_STATIC_BUILD" CGO_LDFLAGS="-lduckdb_bundle -lc++ -L/path/to/libs" go build -tags=duckdb_use_static_lib
```

You can also find these steps in the `Makefile` and the `tests.yaml`.

The DuckDB team also publishes pre-built libraries as part of their [releases](https://github.com/duckdb/duckdb/releases).
The published zipped archives contain libraries for DuckDB core, the third-party libraries, and the default extensions.
When linking, you might want to bundle these libraries into a single archive first.
You can use any archive tool (e.g., `ar`).
DuckDB's `bundle-library` Makefile target contains an example of `ar`, or you can look at the Docker file [here](https://github.com/duckdb/duckdb/issues/17312#issuecomment-2885130728).

#### Note on FreeBSD

Starting with `v2`, duckdb-go drops pre-built FreeBSD support.
This change is because DuckDB does not publish any bundled FreeBSD libraries.
Thus, you must build your static library for FreeBSD using the steps above.

### Linking a Dynamic Library

Alternatively, you can dynamically link DuckDB by passing `-tags=duckdb_use_lib` to `go build`.
You must have a copy of `libduckdb` available on your system (`.so` on Linux or `.dylib` on macOS),
which you can download from the DuckDB [releases page](https://github.com/duckdb/duckdb/releases).

For example:

```sh
# On Linux.
CGO_ENABLED=1 CGO_LDFLAGS="-lduckdb -L/path/to/libs" go build -tags=duckdb_use_lib main.go
LD_LIBRARY_PATH=/path/to/libs ./main

# On MacOS.
CGO_ENABLED=1 CGO_LDFLAGS="-lduckdb -L/path/to/libs" go build -tags=duckdb_use_lib main.go
DYLD_LIBRARY_PATH=/path/to/libs ./main
```

You can also find these steps in the `Makefile` and the `tests.yaml`.

## Notes and FAQs

**`undefined: conn`**

Some people encounter an `undefined: conn` error when building this package.
This error is due to the Go compiler determining that CGO is unavailable.
This error can happen due to a few issues.

The first cause, as noted in the [comment here](https://github.com/marcboeker/go-duckdb/issues/275#issuecomment-2355712997),
might be that the `buildtools` are not installed.
To fix this for ubuntu, you can install them using:

```
sudo apt-get update && sudo apt-get install build-essential
```

Another cause can be cross-compilation since the Go compiler automatically disables CGO when cross-compiling.
To enable CGO when cross-compiling, use `CC={C cross compiler} CGO_ENABLED=1 {command}` to force-enable CGO and set the right cross-compiler.

**`TIMESTAMP vs. TIMESTAMP_TZ`**

In the C API, DuckDB stores both `TIMESTAMP` and `TIMESTAMP_TZ` as `duckdb_timestamp`, which holds the number of
microseconds elapsed since January 1, 1970, UTC (i.e., an instant without offset information).
When passing a `time.Time` to duckdb-go, duckdb-go transforms it to an instant with `UnixMicro()`,
even when using `TIMESTAMP_TZ`. Later, scanning either type of value returns an instant, as SQL types do not model
time zone information for individual values.

**Connection lifetime**

Temporary objects and state, such as temporary tables, are scoped to connections.
When closing a connection, Go's `database.sql` pooling logic might cache it as an idle connection,
instead of invoking its clean-up code by closing the connection.
That behavior can lead to, e.g., temporary tables persisting longer than expected.
To disable keeping idle connections alive, use `db.SetMaxIdleConns(0)`.

## Memory Allocation

DuckDB lives in process.
Therefore, all its memory lives in the driver.
All allocations live in the host process, which is the Go application.
Especially for long-running applications, it is crucial to call the corresponding `Close`-functions as specified in [database/sql](https://godoc.org/database/sql).

Additionally, it is crucial to call `Close()` on the database and/or connector of a persistent DuckDB database.
That way, DuckDB synchronizes all changes from the WAL to its persistent storage.

The following is a list of examples of `Close()`-functions.

```go
db, err := sql.Open("duckdb", "")
defer db.Close()

conn, err := db.Conn(context.Background())
defer conn.Close()

rows, err := conn.QueryContext(context.Background(), "SELECT 42")
// Alternatively, rows.Next() has to return false.
rows.Close()

appender, err := duckdb.NewAppenderFromConn(conn, "", "test")
defer appender.Close()

c, err := duckdb.NewConnector("", nil)
// Optional, if passed to sql.OpenDB.
defer c.Close()
```

## DuckDB Appender API

If you want to use the [DuckDB Appender API](https://duckdb.org/docs/data/appender.html), you can obtain a new `Appender` by passing a DuckDB connection to `NewAppenderFromConn()`.
See `examples/appender/main.go` for a complete example.

```go
c, err := duckdb.NewConnector("test.db", nil)
defer c.Close()

conn, err := c.Connect(context.Background())
defer conn.Close()

// Obtain an appender from the connection.
// NOTE: The table 'test_tbl' must exist in test.db.
appender, err := NewAppenderFromConn(conn, "", "test_tbl")
defer appender.Close()

err = appender.AppendRow(...)
```

## DuckDB Profiling API

This section describes using the [DuckDB Profiling API](https://duckdb.org/docs/dev/profiling.html).
DuckDB's profiling information is connection-local.
The following example walks you through the necessary steps to obtain the `ProfilingInfo` type, which contains all available metrics.
Please refer to the [DuckDB documentation](https://duckdb.org/docs/dev/profiling.html) on configuring and collecting specific metrics.

- First, you need to obtain a connection.
- Then, you enable profiling for the connection.
- Now, for each subsequent query on this connection, DuckDB will collect profiling information.
  - Optionally, you can turn off profiling at any point.
- Next, you execute the query for which you want to obtain profiling information.
- Finally, directly after executing the query, retrieve any available profiling information.

```Go
db, err := sql.Open("duckdb", "")
defer db.Close()

conn, err := db.Conn(context.Background())
defer conn.Close()

_, err = conn.ExecContext(context.Background(), `PRAGMA enable_profiling = 'no_output'`)
_, err = conn.ExecContext(context.Background(), `PRAGMA profiling_mode = 'detailed'`)

res, err := conn.QueryContext(context.Background(), `SELECT 42`)
defer res.Close()

info, err := GetProfilingInfo(conn)

_, err = conn.ExecContext(context.Background(), `PRAGMA disable_profiling`)
```

## DuckDB Apache Arrow Interface

The [DuckDB Arrow Interface](https://duckdb.org/docs/api/c/api#arrow-interface) is a heavy dependency.
Starting with `v2`, the DuckDB Arrow Interface is opt-in instead of opt-out.
If you want to use it, you can enable it by passing `-tags=duckdb_arrow` to `go build`.

```sh
go build -tags="duckdb_arrow"
```

You can obtain a new `Arrow` by passing a DuckDB connection to `NewArrowFromConn()`.

```go
c, err := duckdb.NewConnector("", nil)
defer c.Close()

conn, err := c.Connect(context.Background())
defer conn.Close()

// Obtain the Arrow from the connection.
arrow, err := duckdb.NewArrowFromConn(conn)

rdr, err := arrow.QueryContext(context.Background(), "SELECT * FROM generate_series(1, 10)")
defer rdr.Release()

for rdr.Next() {
  // Process each record.
}
```

> [!WARNING]  
> Arrow connections are not safe for concurrent use, and do not benefit from `database/sql` connection pooling.

## DuckDB Extensions

`duckdb-go` relies on the [`duckdb-go-bindings` module](https://github.com/duckdb/duckdb-go-bindings).
Any pre-built library in `duckdb-go-bindings` statically links the default extensions: ICU, JSON, Parquet, and Autocomplete.
Additionally, automatic extension loading is enabled.

## Releasing a New DuckDB Version

1. Create a new branch.
2. Update `duckdb-go-bindings` via `go get github.com/duckdb/duckdb-go-bindings@latest`.
3. Run `go mod tidy`.
4. Update `DUCKDB_VERSION` in `Makefile`.
5. Update the latest version in `README.md`.
6. Commit and PR changes.
7. Push a new tagged release, `v2.MAJOR_MINOR_PATCH.x`, e.g. `v2.10500.0` for DuckDB 1.5.0.

```
git tag <tagname>
git push origin <tagname>
```
