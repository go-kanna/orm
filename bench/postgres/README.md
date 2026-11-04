# Benchmarks on PostgreSQL

[![日本語](https://img.shields.io/badge/lang-%E6%97%A5%E6%9C%AC%E8%AA%9E-blue.svg)](README.ja.md)

kanna-orm next to pgx alone and to the ORMs and generators Go programs commonly use, each reading and writing the same
rows of the same database, as its docs would have it.

```sh
make bench                                    # fill a schema of the database, check the subjects agree, and run them
make bench BENCH_FLAGS='-benchmem -count=10'  # ten runs of each, for benchstat
make bench-local                              # the same, run here against $(BENCH_POSTGRES) rather than in a container
make bench-gen                                # regenerate the code of sqlc and ent
```

`make bench` runs the benchmarks in the `bench` container of `compose.yaml`, on the Go of `go.mod`'s toolchain line,
next to the PostgreSQL of `make db-up`, which it starts if need be: a round trip there stays within the network between
the two containers, where one through the host would cost more than most subjects themselves do.

## Subjects

| Subject         | What                                                       | Runs on                            |
|-----------------|------------------------------------------------------------|------------------------------------|
| `pgx`           | pgx v5.11.0, with SQL and scanning written by hand         | pgxpool                            |
| `sqlc`          | sqlc v1.31.1                                               | pgxpool                            |
| `kanna(pgx)`    | kanna-orm of this repository, generated with `-driver pgx` | pgxpool                            |
| `kanna(sql.DB)` | the same, generated with `-driver database/sql`            | pgx/stdlib                         |
| `gorm`          | GORM v1.31.2, with gorm.io/driver/postgres v1.6.3          | pgx/stdlib                         |
| `bun`           | bun v1.3.0                                                 | pgx/stdlib, in the simple protocol |
| `ent`           | ent v0.14.6, with the `sql/upsert` feature                 | pgx/stdlib                         |

Each subject is a package of its own here, with its own models, written as its docs would have it. Where they leave a
choice:

- GORM writes with `SkipDefaultTransaction`. Otherwise it begins and commits a transaction around each write, two round
  trips none of the others makes.
- bun runs pgx in the simple protocol, as [bun's docs](https://bun.uptrace.dev/postgres/) do: bun puts the arguments
  into the query itself, so there is nothing to prepare.
- sqlc reads `timestamptz` into `time.Time`, and a column that may be NULL into a pointer, as the others do, rather than
  into pgtype's types. Its catalog does not know `unnest` of several arrays, so it inserts many rows by an `unnest` of
  each.
- The rest is as each comes. In particular, pgx prepares each statement once a connection, for every subject but bun.

## Operations

`migrate` fills a schema `bench`, apart from the tables of the example, with 1,000 customers, 10,000 orders, a third of
them open, and five lines of each order.

| Benchmark       | Reads or writes                                                                                                                                               |
|-----------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Get`           | an order by its primary key, another each time                                                                                                                |
| `List`          | the latest 20 open orders placed since a day: `status = $1 AND placed_at >= $2 ORDER BY placed_at DESC, id DESC LIMIT 20`                                     |
| `ListWithLines` | the same, and their 100 lines by a second query: `WithLines` of kanna-orm and ent, `Preload` of GORM, `Relation` of bun, and by hand for pgx and sqlc         |
| `KeysetPage`    | the 20 open orders after the last of `List`: `(placed_at, id) < ($2, $3)`, which kanna-orm writes for `After`                                                 |
| `Insert`        | a customer, reading back their ID                                                                                                                             |
| `InsertMany`    | 100 lines of an order in one statement, reading back their IDs: by `unnest` for pgx, sqlc, and kanna-orm, and by `VALUES (...), (...)` for GORM, bun, and ent |
| `Upsert`        | a customer met by their email, `ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name`, reading back their ID                                                |

- The benchmarks run after `TestSameReads` and `TestSameWrites`, which check that every subject reads the same rows, in
  the same order, and writes what the others do.
- A write benchmark deletes what it wrote, and vacuums the table, before the next subject runs.
- kanna-orm reads back every column of a row it writes, where the others read back its ID alone.

## Results

These are the numbers of kanna-orm as of [#132](https://github.com/go-kanna/orm/pull/132), taken on 2026-11-04 with
`make bench BENCH_FLAGS='-benchmem -count=10'`: on Go 1.26.9 in the `bench` container, next to PostgreSQL 18.6 in its
own, under OrbStack on an Apple M5 Pro with macOS 26.7.1, all of its 18 CPUs given to OrbStack, from one goroutine.
They are not kept up to date; `make bench` gives those of the code at hand, on your machine. Each is the median of ten
runs, by benchstat.

The allocations are the subject's own work, and come out the same from run to run:

| allocs/op     | `pgx` | `sqlc` | `kanna(pgx)` | `kanna(sql.DB)` | `gorm` | `bun` | `ent` |
|---------------|------:|-------:|-------------:|----------------:|-------:|------:|------:|
| Get           |    16 |     16 |           16 |              42 |     85 |    52 |   120 |
| List          |   100 |    101 |           75 |             185 |    343 |   214 |   441 |
| ListWithLines |   518 |    528 |          246 |             678 |   1905 |  1267 |  1696 |
| KeysetPage    |   101 |    102 |           85 |             195 |    346 |   214 |   455 |
| Insert        |    17 |     17 |           21 |              45 |     71 |    35 |    78 |
| InsertMany    |   335 |    335 |          241 |             462 |    981 |   235 |  3688 |
| Upsert        |    15 |     15 |           19 |              42 |     77 |    33 |    96 |

The time includes the database's, and varies from run to run by the percentage after it, the 95% confidence interval:

| µs/op         |      `pgx` |     `sqlc` | `kanna(pgx)` | `kanna(sql.DB)` |      `gorm` |      `bun` |      `ent` |
|---------------|-----------:|-----------:|-------------:|----------------:|------------:|-----------:|-----------:|
| Get           |  14.3 ± 3% |  14.4 ± 3% |    14.4 ± 2% |      16.6 ± 11% |  25.3 ± 24% |  31.0 ± 3% |  24.0 ± 5% |
| List          |  26.1 ± 3% |  24.2 ± 3% |    25.4 ± 3% |       38.4 ± 4% |   62.2 ± 2% |  63.6 ± 4% |  47.5 ± 2% |
| ListWithLines |  86.1 ± 2% | 98.0 ± 14% |    89.6 ± 2% |      143.8 ± 3% |  215.9 ± 2% | 205.6 ± 5% | 157.6 ± 4% |
| KeysetPage    |  27.2 ± 4% |  26.0 ± 5% |    26.8 ± 3% |       38.9 ± 4% |   60.6 ± 3% |  65.6 ± 3% |  48.2 ± 2% |
| Insert        |  15.4 ± 3% |  15.5 ± 8% |    16.0 ± 4% |       18.6 ± 8% |   26.3 ± 7% | 38.2 ± 20% | 21.3 ± 10% |
| InsertMany    | 510.9 ± 3% | 524.5 ± 2% |   552.9 ± 2% |      587.8 ± 3% | 718.1 ± 12% | 606.9 ± 3% | 933.1 ± 4% |
| Upsert        |  18.5 ± 3% |  18.8 ± 3% |    19.3 ± 1% |       24.0 ± 3% |   30.9 ± 2% |  42.6 ± 8% |  27.6 ± 3% |

### Reading them

- A round trip between the two containers is short, some 14 µs for the whole of `Get` through pgx, so the subjects'
  own work shows. A database across a network is many times farther, and the same differences are smaller beside it.
- `pgx` is the floor, and `sqlc` and `kanna(pgx)`, which run SQL much as it is written, stay near it. kanna-orm is
  within about a µs of sqlc on every read, the scope it builds, and renders into SQL, for each query included, and
  allocates no more: it reads the rows into one row made before the loop, where sqlc declares one in the loop for each
  row. In `ListWithLines`, which reads the lines of the orders by their keys in one query, and hands them out by the
  number of each key, it allocates half as much as sqlc, and takes no longer.
- On `Insert` and `Upsert`, kanna-orm reads back every column of the row, where pgx and sqlc read back its ID: that is
  what it allocates besides, and some half a µs.
- `kanna(pgx)` and `kanna(sql.DB)` run the same SQL, so the difference between them is that of database/sql. Compare
  `gorm`, `bun`, and `ent` with `kanna(sql.DB)`, which goes through database/sql as they do.
- `bun` sends each query as text, its arguments in it, for the server to parse and plan each time.
- In `InsertMany`, the database's work is most of the time, and kanna-orm, reading back every column of the hundred
  rows where pgx and sqlc read back their IDs, takes some 5% more than sqlc.
