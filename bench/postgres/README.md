# Benchmarks on PostgreSQL

kanna-orm next to pgx alone and to the ORMs and generators Go programs commonly use, each reading and writing the same
rows of the same database, as its docs would have it.

```sh
make db-up
make bench                                    # fill a schema of the database, check the subjects agree, and run them
make bench BENCH_FLAGS='-benchmem -count=6'   # six runs of each, for benchstat
make bench-gen                                # regenerate the code of sqlc and ent
```

## Subjects

| Subject     | What                                                     | Runs on                            |
|-------------|----------------------------------------------------------|------------------------------------|
| `pgx`       | pgx v5.11.0, with SQL and scanning written by hand       | pgxpool                            |
| `sqlc`      | sqlc v1.31.1                                             | pgxpool                            |
| `kanna`     | kanna-orm of this repository, generated with `-driver pgx` | pgxpool                          |
| `kanna-sql` | the same, generated with `-driver database/sql`          | pgx/stdlib                         |
| `gorm`      | GORM v1.31.2, with gorm.io/driver/postgres v1.6.3        | pgx/stdlib                         |
| `bun`       | bun v1.3.0                                               | pgx/stdlib, in the simple protocol |
| `ent`       | ent v0.14.6, with the `sql/upsert` feature               | pgx/stdlib                         |

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

| Benchmark       | Reads or writes                                                                                                                                                       |
|-----------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Get`           | an order by its primary key, another each time                                                                                                                        |
| `List`          | the latest 20 open orders placed since a day: `status = $1 AND placed_at >= $2 ORDER BY placed_at DESC, id DESC LIMIT 20`                                             |
| `ListWithLines` | the same, and their 100 lines by a second query: `WithLines` of kanna-orm and ent, `Preload` of GORM, `Relation` of bun, and by hand for pgx and sqlc                 |
| `KeysetPage`    | the 20 open orders after the last of `List`: `(placed_at, id) < ($2, $3)`, which kanna-orm writes for `After`                                                         |
| `Insert`        | a customer, reading back their ID                                                                                                                                     |
| `InsertMany`    | 100 lines of an order in one statement, reading back their IDs: by `unnest` for pgx, sqlc, and kanna-orm, and by `VALUES (...), (...)` for GORM, bun, and ent         |
| `Upsert`        | a customer met by their email, `ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name`, reading back their ID                                                        |

- `make bench` runs `TestSameReads` and `TestSameWrites` first. They check that every subject reads the same rows, in
  the same order, and writes what the others do.
- A write benchmark deletes what it wrote, and vacuums the table, before the next subject runs.
- kanna-orm reads back every column of a row it writes, where the others read back its ID alone.

## Results

Taken on 2026-10-30 with `GOTOOLCHAIN=go1.26.9 make bench BENCH_FLAGS='-benchmem -count=6'`, on an Apple M5 Pro with
macOS 26.7.1, against the PostgreSQL 18.6 of `compose.yaml` in Docker on the same machine, from one goroutine. Each
number is the median of six runs, by benchstat.

| µs/op         | `pgx` | `sqlc` | `kanna` | `kanna-sql` | `gorm` | `bun` | `ent` |
|---------------|------:|-------:|--------:|------------:|-------:|------:|------:|
| Get           |    77 |     77 |      78 |          80 |     83 |    90 |    83 |
| List          |    85 |     84 |      86 |          92 |    109 |   110 |    97 |
| ListWithLines |   194 |    197 |     198 |         222 |    279 |   286 |   279 |
| KeysetPage    |    86 |     86 |      87 |          94 |    110 |   114 |    96 |
| Insert        |    77 |     77 |      78 |          81 |     84 |    96 |    82 |
| InsertMany    |   616 |    608 |     668 |         655 |    713 |   770 |   856 |
| Upsert        |   100 |     80 |      82 |          88 |     93 |   124 |    90 |

| allocs/op     | `pgx` | `sqlc` | `kanna` | `kanna-sql` | `gorm` | `bun` | `ent` |
|---------------|------:|-------:|--------:|------------:|-------:|------:|------:|
| Get           |    16 |     16 |      24 |          48 |     85 |    52 |   120 |
| List          |   100 |    101 |     125 |         215 |    343 |   214 |   441 |
| ListWithLines |   518 |    528 |     566 |         877 |   1908 |  1268 |  1697 |
| KeysetPage    |   101 |    102 |     135 |         225 |    346 |   214 |   455 |
| Insert        |    17 |     17 |      21 |          43 |     71 |    35 |    78 |
| InsertMany    |   335 |    335 |     439 |         561 |    982 |   236 |  3689 |
| Upsert        |    15 |     15 |      19 |          41 |     77 |    33 |    96 |

| B/op          | `pgx` | `sqlc` | `kanna` | `kanna-sql` | `gorm` |   `bun` |   `ent` |
|---------------|------:|-------:|--------:|------------:|-------:|--------:|--------:|
| Get           |  1067 |   1035 |    1379 |        2086 |   5755 |    6910 |    4997 |
| List          | 10820 |   8612 |   12052 |       12618 |  13466 |   15683 |   16227 |
| ListWithLines | 37527 |  49048 |   52568 |       50726 |  65811 |   53090 |   60168 |
| KeysetPage    | 10852 |   8644 |   12651 |       13282 |  13801 |   15695 |   16723 |
| Insert        |   851 |    851 |    1099 |        1936 |   5881 |    6290 |    3200 |
| InsertMany    | 23796 |  23795 |   47275 |       44462 |  91890 |   20846 |  251708 |
| Upsert        |   819 |    819 |    1084 |        1912 |   6553 |    6386 |    4136 |

### Reading them

- Most of the time is the database's: a query costs a round trip to Docker of some 70 µs, against which the subjects
  differ by a few to tens of µs. Times vary by up to about 20% from run to run, as `Upsert` of `pgx` does, slower than
  `sqlc` running the same statement; a difference of a few µs says little. Allocations are the subject's own work, and
  steady from run to run.
- `pgx` is the floor, and `sqlc` and `kanna`, which run SQL much as it is written, stay near it. kanna-orm's few more
  allocations are its scope, built for each query, and in a write, the row it reads back whole.
- `kanna` and `kanna-sql` run the same SQL, so the difference between them is that of database/sql. Compare `gorm`,
  `bun`, and `ent` with `kanna-sql`, which goes through database/sql as they do.
- `bun` sends each query as text, its arguments in it, for the server to parse and plan each time.
- In `InsertMany`, `unnest` sends three arrays where `VALUES` sends 300 values; how much that matters depends more on
  the distance to the database than on the subject.

### On Go 1.27

On Go 1.27.3, the subjects on database/sql read rows much more slowly, while those on pgx itself do not change. From
Go 1.27, pgx/stdlib v5.11.0 implements `driver.RowsColumnScanner`, through which database/sql scans each column; reading
the 20 rows of `List` through it took 48 µs, against 6 µs on Go 1.26.9 and 3 µs through pgx. A CPU profile has the time
waiting, not scanning; why is not known yet.

| µs/op on Go 1.27.3 | `pgx` | `sqlc` | `kanna` | `kanna-sql` | `gorm` | `bun` | `ent` |
|--------------------|------:|-------:|--------:|------------:|-------:|------:|------:|
| List               |    84 |     90 |      89 |         141 |    174 |   141 |   119 |
| ListWithLines      |   193 |    199 |     199 |         297 |    398 |   361 |   286 |
| KeysetPage         |    87 |     86 |      88 |         154 |    175 |   143 |   118 |
