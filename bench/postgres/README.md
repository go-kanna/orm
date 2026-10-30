# Benchmarks on PostgreSQL

kanna-orm next to pgx alone and to the ORMs and generators Go programs commonly use, each reading and writing the same
rows of the same database, as its docs would have it.

```sh
make bench                                    # fill a schema of the database, check the subjects agree, and run them
make bench BENCH_FLAGS='-benchmem -count=6'   # six runs of each, for benchstat
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

These are the numbers of kanna-orm v0.0.3, taken on 2026-10-30 with `make bench BENCH_FLAGS='-benchmem -count=6'`: on
Go 1.26.9 in the `bench` container, next to PostgreSQL 18.6 in its own, under OrbStack on an Apple M5 Pro with macOS
26.7.1, all of its 18 CPUs given to OrbStack, from one goroutine. They are not kept up to date; `make bench` gives those
of the code at hand, on your machine. Each is the median of six runs, by benchstat.

The allocations are the subject's own work, and come out the same from run to run:

| allocs/op     | `pgx` | `sqlc` | `kanna(pgx)` | `kanna(sql.DB)` | `gorm` | `bun` | `ent` |
|---------------|------:|-------:|-------------:|----------------:|-------:|------:|------:|
| Get           |    16 |     16 |           24 |              48 |     85 |    52 |   120 |
| List          |   100 |    101 |          125 |             215 |    343 |   214 |   441 |
| ListWithLines |   518 |    528 |          566 |             876 |   1905 |  1267 |  1696 |
| KeysetPage    |   101 |    102 |          135 |             225 |    346 |   214 |   455 |
| Insert        |    17 |     17 |           21 |              44 |     71 |    35 |    79 |
| InsertMany    |   335 |    335 |          439 |             560 |    981 |   235 |  3688 |
| Upsert        |    15 |     15 |           19 |              41 |     77 |    33 |    96 |

The time includes the database's, and varies from run to run by the percentage after it, the 95% confidence interval:

| µs/op         |       `pgx` |      `sqlc` | `kanna(pgx)` | `kanna(sql.DB)` |      `gorm` |       `bun` |       `ent` |
|---------------|------------:|------------:|-------------:|----------------:|------------:|------------:|------------:|
| Get           |   13.3 ± 4% |   13.2 ± 7% |    14.0 ± 3% |       17.6 ± 9% |  23.4 ± 13% |   31.2 ± 3% |  23.5 ± 10% |
| List          |   26.3 ± 3% |   22.8 ± 8% |    26.7 ± 3% |       36.3 ± 6% |   55.2 ± 3% |   55.2 ± 4% |   41.8 ± 4% |
| ListWithLines |   77.6 ± 6% |   88.3 ± 2% |    96.0 ± 9% |      122.5 ± 7% |  198.6 ± 3% |  196.6 ± 8% |  149.0 ± 9% |
| KeysetPage    |   26.0 ± 4% |   24.3 ± 3% |    28.6 ± 2% |       37.8 ± 4% |   56.3 ± 2% |   61.4 ± 8% |   43.7 ± 3% |
| Insert        |   15.2 ± 8% |   15.1 ± 5% |    16.2 ± 5% |       17.7 ± 2% |   22.8 ± 7% |   33.3 ± 4% |   20.4 ± 5% |
| InsertMany    |  546.4 ± 4% |  533.4 ± 9% |   510.5 ± 6% |      549.9 ± 1% |  627.7 ± 2% |  630.1 ± 3% |  856.3 ± 4% |
| Upsert        |   18.6 ± 6% |   18.8 ± 8% |    19.3 ± 3% |       22.1 ± 2% |   28.5 ± 3% |   39.5 ± 2% |   26.2 ± 3% |

### Reading them

- A round trip between the two containers is short, some 13 µs for the whole of `Get` through pgx, so the subjects'
  own work shows. A database across a network is many times farther, and the same differences are smaller beside it.
- `pgx` is the floor, and `sqlc` and `kanna(pgx)`, which run SQL much as it is written, stay near it. kanna-orm is
  within about a µs of pgx on `Get`, `List`, `Insert`, and `Upsert`, and some 3 µs over it on `KeysetPage`; its few
  more allocations are the scope it builds, and renders into SQL, for each query. `ListWithLines` is where it costs the
  most over pgx, some 18 µs, though with the widest spread.
- `kanna(pgx)` and `kanna(sql.DB)` run the same SQL, so the difference between them is that of database/sql. Compare
  `gorm`, `bun`, and `ent` with `kanna(sql.DB)`, which goes through database/sql as they do.
- `bun` sends each query as text, its arguments in it, for the server to parse and plan each time.
- In `InsertMany`, the database's work is most of the time, and kanna-orm reads back every column no slower than the
  others read back the ID.
