# orm

[![CI](https://github.com/go-kanna/orm/actions/workflows/ci.yaml/badge.svg)](https://github.com/go-kanna/orm/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/go-kanna/orm/graph/badge.svg)](https://codecov.io/gh/go-kanna/orm)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-kanna/orm.svg)](https://pkg.go.dev/github.com/go-kanna/orm)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/tag/go-kanna/orm?label=release&sort=semver)](https://github.com/go-kanna/orm/tags)
[![日本語](https://img.shields.io/badge/lang-%E6%97%A5%E6%9C%AC%E8%AA%9E-blue.svg)](README.ja.md)

kanna-orm writes the database code behind your repository interfaces. You declare the interface, and a scope type for
each table it reads; kanna-orm writes the implementation, and a method on the scope type for each way a query can be
narrowed, ordered, paged, or made to load what a row refers to. The SQL stays in the generated code as literals, and a
column or a type that does not match the schema stops generation, or compilation, rather than a request.

Part of the [kanna](https://go-kanna.github.io/) family of code generators. Requires Go 1.26, and PostgreSQL
through pgx or database/sql, or MySQL 8.4 or later through database/sql.

## Install

```sh
go get -tool github.com/go-kanna/orm/cmd/kanna-orm
```

The generated code imports packages of `github.com/go-kanna/orm`, which the line above already requires. They depend on
nothing outside the standard library; the driver is yours. Code written by hand uses these:

| Package      | What for                                                                                |
|--------------|-----------------------------------------------------------------------------------------|
| `orm`        | `ErrNotFound`, `ErrInvalidCursor`, and `ErrEmptyChangeSet`, the errors to test for      |
| `orm/scope`  | `Of`, the one field of a scope type declared by hand, and `Raw`, for SQL written in Go  |
| `orm/option` | `WithHook`, `WithClock`, and `Join`, the options a repository's constructor takes       |
| `orm/hook`   | `Hook`, `Query`, and `Execution`, the functions that option takes and what they receive |
| `orm/pgx`    | `DBTX`, what a repository runs on by pgx, which a dependency injector provides          |
| `orm/stdlib` | `DBTX`, what a repository runs on by `database/sql`, likewise                           |

`orm/change` holds `Set`, what the `Of` of a generated change set returns, which code written by hand does not name.
`orm/impl` is what the generated code runs on, and only the generated code imports it.

## Use

kanna-orm reads the schema from a database, not from your migrations. The examples below take a module named
`example.com/shop` on PostgreSQL through pgx, with these tables:

```sql
CREATE TABLE orders (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL,
    status      text NOT NULL,
    placed_at   timestamptz NOT NULL,
    note        text
);

CREATE TABLE order_lines (
    order_id bigint NOT NULL REFERENCES orders ON DELETE CASCADE,
    line_no  integer NOT NULL,
    sku      text NOT NULL,
    quantity integer NOT NULL,
    PRIMARY KEY (order_id, line_no)
);
```

Migrate a database to them, and describe it into a snapshot at the root of your module:

```sh
go tool kanna-orm describe -database-url "$DATABASE_URL" ./...
```

Commit `kanna-orm.snapshot.json` along with the migration: the change to the schema shows in review, and generation
reads the snapshot alone, so it needs no database.

Name the package of your models, and kanna-orm declares a scope type for each of them that a table is named after:

```go
package scopes

//go:generate go tool kanna-orm

//kanna:scopes model-package=../model
```

and the repository the rest of your code depends on:

```go
package repository

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

//go:generate go tool kanna-orm -destination ../postgres

//kanna:repository
type Order interface {
	Get(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	Insert(ctx context.Context, o model.Order) (model.Order, error)
	Delete(ctx context.Context, id model.OrderID) error
}
```

With the models as your domain has them:

```go
package model

import "time"

type OrderID int64

type CustomerID int64

type OrderStatus string

const (
	OrderStatusOpen     OrderStatus = "open"
	OrderStatusShipped  OrderStatus = "shipped"
	OrderStatusCanceled OrderStatus = "canceled"
)

type Order struct {
	ID         OrderID
	CustomerID CustomerID
	Status     OrderStatus
	PlacedAt   time.Time
	Memo       *string `orm:"note"`
	Lines      []OrderLine
}

type OrderLine struct {
	OrderID  OrderID
	LineNo   int
	SKU      string
	Quantity int
}
```

`OrderID` and `CustomerID` are types of their own, so that one is not passed for the other. A table keyed by a `uuid`
is declared the same way: see [Keyed by a uuid](#keyed-by-a-uuid).

`go generate ./...` writes `scopes_gen.go` next to the scope types:

```go
// Code generated by kanna-orm. DO NOT EDIT.

// Orders returns a scope over all the rows of orders.
func Orders() Order { return Order{} }

// StatusNe narrows to the rows whose status is not v.
func (s Order) StatusNe(v model.OrderStatus) Order {
	return Order{kScope(kRuntime(s.q).Where(`status <> `, v))}
}

// PlacedAtDesc orders the rows by placed_at, latest first.
func (s Order) PlacedAtDesc() Order {
	return Order{kScope(kRuntime(s.q).OrderBy(`placed_at`, true))}
}

// WithLines loads Lines, from order_lines by order_id.
func (s Order) WithLines(sub ...OrderLine) Order {
	// ...
}

// ...a method for each column and relation, and Limit, Offset, Or, Not, After, Before, Cursor, and AfterCursor.
```

and the implementation into `../postgres`:

```go
// Code generated by kanna-orm. DO NOT EDIT.

// Order implements repository.Order on PostgreSQL through pgx.
type Order struct{ db kDatabase }

func newOrder(db DBTX, opts ...option.Option) Order {
	return Order{db: kDatabase{dbtx: db, options: impl.NewOptions(opts...)}}
}

// NewOrder returns Order running on db.
func NewOrder(db DBTX, opts ...option.Option) Order { return newOrder(db, opts...) }

func (r Order) Insert(ctx context.Context, o model.Order) (model.Order, error) {
	const q = `INSERT INTO orders (customer_id, status, placed_at, note)
VALUES ($1, $2, $3, $4)
RETURNING id, customer_id, status, placed_at, note`
	var row model.Order
	err := r.db.QueryRow(ctx, q, o.CustomerID, o.Status, o.PlacedAt, o.Memo).
		Scan(&row.ID, &row.CustomerID, &row.Status, &row.PlacedAt, &row.Memo)
	if err != nil {
		return model.Order{}, fmt.Errorf("insert into orders: %w", err)
	}
	return row, nil
}

// ...
```

A `*pgxpool.Pool` is the `DBTX` the constructor takes, and callers chain the scope:

```go
package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/shop/model"
	"example.com/shop/postgres"
	"example.com/shop/repository"
	"example.com/shop/scopes"
)

func listRecent(ctx context.Context, pool *pgxpool.Pool) ([]model.Order, error) {
	var repo repository.Order = postgres.NewOrder(pool)

	lastMonth := time.Now().AddDate(0, -1, 0)
	return repo.List(ctx, scopes.Orders().
		StatusNe(model.OrderStatusCanceled).
		PlacedAtGte(lastMonth).
		WithLines().
		PlacedAtDesc().
		Limit(20))
}
```

A declaration that does not match the schema stops generation, rather than the first request that meets it:

```console
$ go generate ./...
./scopes.go:6:1: error: Order: the snapshot has no table order
  did you mean orders?
../model/order.go:12:2: error: Order.Memo: orders.note may be NULL, which a string cannot hold
  declare it as *string, or tag it orm:",nullzero" to read NULL as the empty string
./order_repository.go:12:2: error: ListByCustomer: model.Order has no field for the parameter custmerID
  did you mean customerID?
./order_repository.go:14:2: error: GetByStatus: status is not a unique key of orders, so Get cannot return one row
  use List, or add a unique index on orders (status)
```

The repository interface names `scopes.Order`, which does not exist until `//kanna:scopes` generates it, so the code
does not compile before the first run. That does not stop the run: kanna-orm knows the type from that directive, and
`go generate` can reach the two packages in either order. Code may also call a method that is generated before it is:
errors saying only that a method the run generates is missing do not stop it.

With two or more kanna generators, run them with [`go tool kanna gen`](https://github.com/go-kanna/kanna), which runs
them in the order they need.

## The snapshot

`describe` connects to the database and records, for each table, its columns, with their types, whether they may be
NULL, and whether the database assigns them itself, as an identity, serial, or AUTO_INCREMENT column does; its primary
key; its unique keys; and its foreign keys. It also prepares each [hand-written query and scope](#hand-written-sql) in
the packages it is given, and in the directories they name that hold no package yet, as the destination of the
repositories does before their first generation, and records the types of their parameters and of their result
columns. Nothing it runs writes.

The database is the one `-database-url` names, or else `$DATABASE_URL`:

- `postgres://user:password@host:5432/database` is PostgreSQL, through pgx. The tables are those of the current
  schema, which `?search_path=` chooses. The SQL generated names a table without its schema, so the application's
  connections need the same search path.
- `mysql://user:password@host:3306/database` is MySQL, through go-sql-driver/mysql, which takes the options after `?`.
  The tables are those of the database the URL names.

Views and materialized views are not recorded: read one by a [query written by hand](#hand-written-sql), which
`describe` prepares as it does any other.

The types are what the database itself answers, so kanna-orm has no SQL parser of its own to disagree with it. MySQL
does not describe the result of a statement it prepares, so there, `describe` reads the columns of the statement
wrapped as `SELECT t.* FROM (...) AS t LIMIT 0`, which returns no rows. On PostgreSQL, a type is named as SQL writes
it: one of `pg_catalog` by its name, as `int8`, and another by its schema and its name, quoted where they need it, as
`public."OrderState"`, so that the SQL generated names it whatever the search path. A domain is named as its base type.

The snapshot records what generation needs, and no more, so `describe -check` tells that the code was generated from
the schema there is, not that two schemas are the same:

| Recorded                                                                                   | Not recorded                              |
|--------------------------------------------------------------------------------------------|-------------------------------------------|
| a column's type, with the length or precision it declares: `varchar(100)`, `numeric(10,2)` | what a default is, only that there is one |
| whether a column may be NULL, has a default, or is assigned by the database                | CHECK constraints                         |
| primary keys, unique keys, and foreign keys                                                | the conditions of partial indexes         |
| the types of each hand-written query's parameters and result columns                       | triggers, collations, and comments        |

A migration changing only what is not recorded leaves the snapshot as it was. To review those, or to compare two
databases, use the tools of your migrations.

The snapshot is JSON with one column to a line, in a fixed order, so that a migration shows as the lines it changes:

```diff
     "orders": {
       "columns": [
         {"name": "id", "type": "uuid", "not_null": true},
         {"name": "customer_id", "type": "uuid", "not_null": true},
         {"name": "status", "type": "text", "not_null": true},
-        {"name": "placed_at", "type": "timestamptz", "not_null": true}
+        {"name": "placed_at", "type": "timestamptz", "not_null": true},
+        {"name": "note", "type": "text"}
       ],
```

A module has one snapshot, `kanna-orm.snapshot.json` at its root, which generation finds by walking up from the
package. A module using two databases describes each into a file of its own, and names it with `-snapshot` on both
commands.

In CI, describe a freshly migrated database with `-check`, which fails when the snapshot is out of date:

```console
$ go tool kanna-orm describe -check ./...
./kanna-orm.snapshot.json: error: out of date with the database
  orders.note (text) is in the database but not in the snapshot
  run go tool kanna-orm describe to update it
```

## Scope types

A scope type is a struct whose only field is an unexported `scope.Of[M]`, `M` being the model the table's rows are read
into, so that nothing but its methods narrows it. Each table kanna-orm reads or writes has one, and each model belongs
to one table: the scope type is the only place where the two are tied together.

`//kanna:scopes model-package=../model` declares one, in `scopes_gen.go`, for each struct of the package of the models
that a table of the snapshot is named after: the struct's name in snake case with its last word made plural, as
`order_lines` for `OrderLine`, `categories` for `Category`, and `people` for `Person`. `model-package=` takes an import
path of the module or a path relative to the file, and more than one separated by commas, as
`model-package=../model,../billing`. `model-package=.` names the directive's own package, where a scope type is named
after its model and `Scope`, as `OrderScope`, apart from the model. A struct no table is named after, such as a value
type, gets none, and neither does a table no struct is named after, such as one between two others. So a struct of those
packages becomes a model once a table of its name is made: keep the structs that are not, such as those a query returns,
in a package of their own. Methods of your own are written on the type as on any other, in another file of the package.
One narrows the scope by the methods kanna-orm writes, as `Open` below does; by SQL of a shape of its own in a `.sql`
file, which `describe` checks (see [Hand-written SQL](#hand-written-sql)); or, for SQL whose shape the program chooses
as it runs, by `Raw`, as `InMonths` does, which nothing checks before it runs. `Raw` has `Where` and `OrderBy` alone,
each returning the scope with all it held before. `Where` takes the SQL of its condition in pieces marked as
`scope.SQL`, and every other part, a string too, as a value it binds to a placeholder, so that no value becomes SQL. A
condition without SQL, or with two values and no SQL between them, is an error, which every query of the scope returns.

```go
func (s Order) Open() Order { return s.StatusNe("closed") }

func (s Order) InMonths(months []int) Order {
	return Order{s.q.Raw().Where(scope.SQL("EXTRACT(MONTH FROM placed_at) = ANY("), months, scope.SQL(")"))}
}
```

A scope type of a name of your own is declared by hand with `//kanna:scope`, which marks a struct alone: on anything
else it stops generation, as nothing reads it. Its table is the one its model is named after, as with `//kanna:scopes`,
unless `table=` names another, as one in the singular. It wins over the one `//kanna:scopes` would imply of its model or
its table in its own package; one of another package stops generation, as a table has one scope type:

```go
//kanna:scope table=person
type Person struct{ q scope.Of[model.Person] }

// People is the empty scope of person, under a name of its own.
func People() Person { return Person{} }
```

A scope type declared by hand gets no function returning the empty scope, as its name and its table are off the
conventions the function's name would follow. Its zero value, `Person{}`, is the empty scope, and a function of a name
of your own is one line, as `People` above.

A model that a relation reaches but no scope type names stops generation:

```console
../model/order_line.go:9:2: error: OrderLine.Product: no scope type is declared for model.Product
  declare one where products are queried: //kanna:scope table=products
```

For each scope type, kanna-orm writes:

- the type, when `//kanna:scopes` implies it;
- a function returning the empty scope, named after the table, `Orders()` for `orders`, for a type `//kanna:scopes`
  implies. When the name is the type's own, as for a table whose name is the same singular and plural, or is taken
  otherwise, declare the type by hand;
- a method for each column, each relation, and each [hand-written scope](#hand-written-sql);
- `Limit`, `Offset`, `Or`, `Not`, `After`, `Before`, `Cursor`, and `AfterCursor`.

Every method returns a new scope and leaves its receiver as it was, so a scope built halfway can be shared:
`base.Limit(10)` and `base.Limit(20)` do not see each other's limit. The zero value, `Order{}`, is the empty scope too.

### Columns and fields

A field holds the column named as the field is, in snake case: `CustomerID` holds `customer_id`, and `PlacedAt` holds
`placed_at`. The conversion is mechanical, so a field named otherwise says so with a tag:

| Tag                             | Meaning                                                                         |
|---------------------------------|---------------------------------------------------------------------------------|
| *(none)*                        | the column named as the field is, in snake case                                 |
| `orm:"note"`                    | the column `note`                                                               |
| `orm:"note,nullzero"`           | a nullable string column: read NULL as the zero string and write the zero string as NULL |
| `orm:",default"`                | omit this column from generated INSERT statements and use its database default |
| `orm:",insertonly"`             | write this column by an insert alone: an update, and an upsert meeting its row, leave it as it was |
| `orm:",readonly"`               | only read this column, which the database writes on every write, as a trigger does |
| `orm:",created"`                | the time a row is created at, which kanna-orm writes: see [Creation and update times](#creation-and-update-times) |
| `orm:",updated"`                | the time a row is last updated at, which kanna-orm writes: see [Creation and update times](#creation-and-update-times) |
| `orm:"-"`                       | not a column: kanna-orm neither reads nor writes the field                      |
| `orm:"fk=order_id"`             | on a [relation](#relations), the columns of the foreign key it follows, when its names do not tell them |
| `orm:"relation=device_topics"`  | a slice of the values of a column of the related rows: a [scalar relation](#scalar-relations) |

A field with no column, a column that may be NULL held by a field that cannot be nil, and, on a table something inserts
into, a column the database requires a value for and the model has no field for, all stop generation. A column the
model has no field for is otherwise left alone.

Use `nullzero` only when a nullable string column represents absence as the field's zero value. It works with named
string types, including enums implementing `sql.Scanner`: generated code uses `sql.Null[T]`, so NULL bypasses the
enum's non-null scanner, while non-null values still go through it. It applies to table operations and hand-written
query models. Omit the column name, as `orm:",nullzero"`, when the field's snake-case name already matches it.

The [PostgreSQL](example/postgres/model/model.go) and [MySQL](example/mysql/model/model.go) examples use this for the
optional order memo:

```go
Memo string `orm:"note,nullzero"`
```

Their [readMemos step](example/postgres/memos.go) selects orders with `MemoIsNull()` and verifies that empty memos were
stored as SQL NULL and read back as empty strings, while a non-empty memo remains unchanged. `note` is explicit here
because the field is named `Memo`; a field named `Note` would need only `orm:",nullzero"`.

What `nullzero` costs is the difference between NULL and `''`: an empty string is written as NULL, and both NULL and a
`''` already in the table are read as the empty string. So a value read back may not find its row: `MemoEq("")`
compares by `= ''`, which matches no NULL. Narrow to the absent ones with `MemoIsNull()`, and hold the column in a
`*string` when NULL and `''` say different things.

A field's type must be one the driver reads the column's type into: an integer type for `bigint`, a string for `text` or
`citext`, `time.Time` for `timestamptz` or `datetime`, `[]byte` for `bytea` or `blob`, `uuid.UUID` of Go 1.27, a type
declared of it, or a string for `uuid`, and a slice of those for an array of PostgreSQL, which MySQL has none of.
database/sql reads a binary column into a string too, but pgx reads a `bytea` into no string, so a string holding one
stops generation on pgx. MySQL's driver reads a `bit(n)` column as bytes, so hold one in a `[]byte`: a bool, an integer,
or a string holding one stops generation. For a boolean, declare the column `tinyint(1)`, which a bool holds.
PostgreSQL's `bit(n)` and `varbit` are read into a type with a `Scan` method, such as `pgtype.Bits`, on pgx, and into a
string or a `[]byte` too on database/sql, which reads them as text such as `101`: a bool or an integer holding one stops
generation on either, as an integer would read `101` as a hundred and one. On MySQL, a parameter written to a `bit(n)`
may be a bool or a number. A type with `Scan` and `Value` methods converts itself, and fits any column, and a type of
the database kanna-orm does not know, such as an enum or `json`, is taken at the field's word. That word is still one of
a column: hold a `json` or `jsonb` column in a string, a `[]byte` such as `json.RawMessage`, or a type with `Scan` and
`Value` methods that decode and encode the JSON. A struct or a map without them is neither a column nor a relation, and
stops generation. pgx, on its own or under database/sql, writes and reads a slice, a number and a bool as JSON too. It
writes a `time.Time` either way, but reads one only on its own: under database/sql it hands over the JSON text, which
database/sql converts into no `time.Time`, so there a `time.Time` stops generation. A value type of kanna-value holding
a `time.Time` stops generation on either driver, as its `Scan` method gets the text too.
go-sql-driver/mysql writes json from a string or a `[]byte` alone, and database/sql reads it into no other slice and no
`time.Time`, so on MySQL a slice of another type or a `time.Time` stops generation, and so does a number or a bool in a
field of a model. A query written by hand may still read a json holding a number or a bool into one. On either
database, an enum of kanna-value, a pointer to one, or a value type holding one stops generation on a `json` or `jsonb`
column: none of its values is JSON text, and its `Scan` method rejects the JSON text it reads. Hold it in a `text` or
`varchar` column instead.

A field read from a column must also hold every value the column can: a row holding a value beyond the field fails to
scan, and a `float32` read from a `float8` or `double` loses precision without a word. So an integer field is at least
as wide as the column, `int64` for `bigint` and `int32` for `integer`, signed for a signed column, and unsigned or a
bit wider for an unsigned one: `uint64` for `bigint unsigned`, `int64` for `int unsigned`. `int` and `uint` are taken
to be 64 bits wide, as on amd64. Writing needs no such width, as the database rejects a value beyond its column rather
than store another, so a field wider than its column, as an `int` for an `integer`, is fine, and so is a parameter of
any width. A `numeric` or `decimal` column is read into a string, or a type with `Scan` and `Value` methods: a
floating-point field stops generation, as it rounds the values it holds no exact form of, and an integer field reads
only a column of scale 0 every value of which it holds, an `int64` one up to `numeric(18,0)` and an `int32` one up to
`numeric(9,0)`. The scale of a `numeric` declared with no precision, and that of a column a query written by hand
returns, as of a `sum`, is not recorded, so such a column is read into a string alone, or cast in the query. A parameter
written to a `numeric` may be any number.

### Methods for columns

The methods are named after the field, so the column `note`, held by `Memo`, gives `MemoContains`. Each column gets the
methods its type allows:

| Column                                   | Methods, for a field `Status`                                                                                    |
|------------------------------------------|------------------------------------------------------------------------------------------------------------------|
| any                                      | `StatusEq`, `StatusNe`, `StatusIn`, `StatusNotIn`, `StatusAsc`, `StatusDesc`                                     |
| of an ordered type: number, time, string | also `StatusLt`, `StatusLte`, `StatusGt`, `StatusGte`, `StatusBetween`                                           |
| of a string                              | also `StatusLike`; `StatusHasPrefix` and `StatusContains`, which escape `%` and `_`; `StatusILike` on PostgreSQL |
| that may be NULL                         | also `StatusIsNull`, `StatusIsNotNull`                                                                           |

A method takes the field's type: `StatusEq` takes a `model.OrderStatus`, and a value type goes to the database through
its `Value` method, as database/sql would send it. The methods matching a pattern take a string, which no value of the
type need be, and a column held in a slice other than `[]byte`, an array, has no `In` or `NotIn`. On PostgreSQL, a
string column gets the methods matching a pattern only if its type is a text type, such as `text`, `varchar`, or
`citext`, or a domain over one: an enum type, `uuid`, or `jsonb` has no `LIKE`. The comparisons of a column that may be
NULL take the value, not a pointer, so that `= NULL`, which matches nothing, cannot be written; `StatusIsNull` says what
it would have meant.

On PostgreSQL, `StatusIn` and `StatusNotIn` send their values as one array, as in `status = ANY($1)`. pgx cannot write
an array of a type it does not know, so for a column of a type outside `pg_catalog`, such as an enum type, the array
goes as `text[]` and is cast to the column's type: `status = ANY($1::text[]::public.order_status[])`.

A comparison with NULL is neither true nor false, as in SQL, so a row whose column is NULL matches no comparison but
`IsNull`: `MemoNe(v)`, `MemoNotIn(vs...)`, and `Not` of `MemoEq(v)` all leave it out. Keep it with `Or`:

```go
// WHERE ... AND ((note <> $1) OR (note IS NULL))
s := scopes.Orders().Or(scopes.Orders().MemoNe(v), scopes.Orders().MemoIsNull())
```

### Your own scopes

A scope you name is a method you write on the scope type, out of the generated ones. It chains as they do:

```go
// Open narrows to the orders that are not canceled.
func (s Order) Open() Order {
	return s.StatusNe(model.OrderStatusCanceled)
}

// Recent orders by placed_at, latest first, and keeps the first n.
func (s Order) Recent(n int) Order {
	return s.PlacedAtDesc().Limit(n)
}
```

A search narrows a scope one condition at a time:

```go
s := scopes.Orders().Open().Recent(in.PageSize)
if in.CustomerID != nil {
	s = s.CustomerIDEq(*in.CustomerID)
}
if in.Keyword != "" {
	s = s.MemoContains(in.Keyword)
}
orders, err := repo.List(ctx, s)
```

A method of yours named as one kanna-orm would generate stops generation instead of being overwritten.

### Or and Not

`Or` matches the rows that any of its scopes match, each scope's own conditions holding together:

```go
// WHERE ... AND ((status = $1) OR (placed_at < $2))
s := scopes.Orders().Or(
	scopes.Orders().StatusEq(model.OrderStatusPending),
	scopes.Orders().PlacedAtLt(cutoff),
)
```

`Not` matches the rows its scope is false for. A condition comparing a NULL is unknown, not false, so its row matches
neither the scope nor `Not` of it, as in SQL: see [Methods for columns](#methods-for-columns). A scope given to `Or` or
`Not` holds conditions only: one with an order, a limit, or a relation to load makes the query return an error. When
every alternative is a value of one column, `StatusIn` says the same more plainly.

### Relations

A field holding a pointer to another model, or a slice of models or of pointers to them, is a relation. Its names
tell the foreign key it follows, so nothing more is written for it; the snapshot is read only to check that the key is
there, and for the columns it refers to. A relation of one row is a pointer, nil when its row is not loaded or not
there; a model held by value stops generation, as it could not tell either:

| Field of `Order`     | Foreign key, as the names tell it                                                      | Kind         |
|----------------------|----------------------------------------------------------------------------------------|--------------|
| `Customer *Customer` | `orders.customer_id` to `customers`: the field's name and `_id`                        | belongs to   |
| `Shipment *Shipment` | `shipments.order_id` to `orders`, a unique key: the model's name and `_id`             | has one      |
| `Lines []OrderLine`  | `order_lines.order_id` to `orders`: the model's name and `_id`                         | has many     |
| `Tags []Tag`         | through `order_tags`, the model's name and the field's: its `order_id` and `tag_id`    | many to many |

A pointer is whichever of a belongs to and a has one the snapshot has, and a slice whichever of a has many and a many
to many; with both, generation stops rather than pick one. A foreign key of other columns is named by a tag, whose
columns are of the table the kind of the relation says: the model's own for a belongs to, and the related one for a
has one or a has many. One of more columns is named by all of them, as `orm:"fk=order_id,line_no"`.

```go
type OrderLine struct {
	OrderID OrderID
	LineNo  int
	SKU     SKU
	Product *Product `orm:"fk=sku"` // order_lines.sku to products, rather than product_id
}

type Category struct {
	ID       int64
	ParentID *int64
	Parent   *Category                       // categories.parent_id, the field's name
	Children []Category `orm:"fk=parent_id"` // categories.parent_id, rather than category_id
}
```

Generation says what it looked for, and which foreign key of the snapshot would read the relation, if one alone would:

```console
../model/order.go:16:2: error: Order.Buyer: no foreign key of orders.buyer_id to customers reads it, nor a unique one of customers.order_id to orders
  orders.customer_id to customers reads it: tag the field orm:"fk=customer_id"
```

A table between whose names are not those of a many to many, as one referring to a key of two columns, is read as a
model of its own, by a has many and a belongs to. A relation the names cannot tell, as a pointer that a foreign key of
each table, of the same name, could read, is read by SQL written by hand: see [Scopes on a query](#scopes-on-a-query)
and [Adding a value related by other columns](#adding-a-value-related-by-other-columns).

A relation is loaded only when the scope asks for it with its `With` method, which takes scopes of the related rows:
their conditions, their order, and relations of their own to load.

```go
o, err := repo.Get(ctx, id, scopes.Orders().
	WithCustomer().
	WithLines(scopes.OrderLines().
		QuantityGt(0).
		SKUAsc().
		WithProduct()))
```

On PostgreSQL, each relation costs one query, however many rows there are: the call above runs four, one each for
orders, customers, order lines, and products. A relation through a table between costs one too, which joins the table.
The related rows are read by the keys of the rows already read, each key once and numbered, as a table the query joins:
`unnest($1::int8[], $2::text[])` on PostgreSQL, each column of the keys cast to the type of its column, and
`VALUES ROW(?, ?), ...` on MySQL, where the keys go a thousand at a time, each thousand in a query of its own. So the
database compares the keys as SQL does, whatever Go's `==` says: a key of a column that ignores case, as MySQL's
default collation does, finds the rows of another case, and `NaN` finds `NaN`. Each related row comes with the number
of its key, which hands it to the rows of that key, and the rows related to one row come in one query, in the order of
the scope given for them.

- A slice that was loaded is never nil: it is empty when nothing is related. A nil slice was not loaded.
- A pointer is nil both when it was not loaded and when nothing is related. Telling the two apart would take a type of
  kanna-orm's in your model.
- A limit or an offset on the related rows keeps a page of those of each row, as a `LATERAL` subquery would in SQL:
  `WithApprovals(scopes.Approvals().ApprovedAtDesc().IDDesc().Limit(3))` reads the three latest approvals of each
  order. The rows of each key are numbered with `ROW_NUMBER()` in the order of the scope, ended in the primary key of
  the related table, or in that key alone when the scope has none, and the query keeps those of the page; the rows of
  each key are counted apart from those of every other, however the database compares the keys. A page continues by
  `Offset`: `After` and `Before` make the query return an error. For a table without a primary key, the scope needs an
  order of its own.
- A value related by other columns than a foreign key, such as who approved the revision an order is at, is read by a
  query of your own: see [Adding a value related by other columns](#adding-a-value-related-by-other-columns).
- Relations are read, never written: `Insert` and `Update` write the row's own columns, and a row read back holds
  them alone.
- A relation by a foreign key of more than one column is read as one of one column is: the table of the keys has a
  column for each, and the query joins them all.

### Paging

`Limit` and `Offset` page as SQL does. For pages that stay put while rows come and go, give `After` the last row of a
page, and it continues from there in the scope's order. `After`, `Before`, `Cursor`, and `AfterCursor` continue by the
primary key, so a scope type of a table without one, or of a model holding no field of it, has none of them, and the
doc of its function returning the empty scope, or of its `Of` for a type declared by hand, says why:

```go
s := scopes.Orders().Open().PlacedAtDesc().Limit(20)

page, err := repo.List(ctx, s)
// WHERE status <> $1
// ORDER BY placed_at DESC, id DESC LIMIT 20
next, err := repo.List(ctx, s.After(page[len(page)-1]))
// WHERE status <> $1 AND (placed_at, id) < ($2, $3)
// ORDER BY placed_at DESC, id DESC LIMIT 20
```

- A `Limit` or an `Offset` with no order keeps rows in whatever order the database reads them, as SQL does: order a page
  by the scopes for it to say which rows it holds. No page continues from such a page: given a scope without an order, a
  query continuing from a row returns an error, and so do `Cursor` and `AfterCursor`. The order may come from another
  scope of the same call.
- A page continues from one row. Two, as `After` and `Before`, `After` and `AfterCursor`, or one in each of two scopes
  of a call, make the query return an error, rather than the later one take the place of the earlier.
-  When the order does not end in the primary key, it gets the key, in the direction of its last column, in every read
  that orders rows, on the first page as on those `After` and `Before` continue to, so that rows equal in the order come
  in one, the same on every page. Without it, a page that ends among equal rows repeats or skips some of them, without a
  word.
- When every column of the order goes the same way, the condition compares rows, as above. When they do not, and on
  MySQL, it is spelled out: `a < $1 OR (a = $1 AND b < $2)`.
- `Before` goes the other way, to the page before the one a row is the first of. The repository reads that page from
  its end and hands its rows back in the scope's order.
- An order by a column that may be NULL makes a query continuing from a row return an error, and so `Cursor`, since
  the databases put NULL in different places.

To hand the position to a client, `Cursor` encodes it as a string, and `AfterCursor` decodes it into the scope:

```go
s, err := scopes.Orders().Open().PlacedAtDesc().AfterCursor(req.PageToken)
if err != nil {
	return nil, err
}
page, err := h.orders.List(ctx, s.Limit(20))
// ...
res.NextPageToken, err = s.Cursor(page[len(page)-1])
```

A cursor holds the order it was made in and the row's values in that order, as JSON; an empty one is the start, and
one made in another order is an error. Each value is decoded through its type, so a value type checks its rule. A
cursor a client has altered changes where the page starts, and nothing else. A cursor that kanna-orm did not make, that
was made in another order, or whose value its type rejects returns an error wrapping `orm.ErrInvalidCursor`, so a
handler can tell a bad page token from a failure of its own with `errors.Is`.

## Repositories

An interface marked `//kanna:repository` declares what the code above the database may do with it. Its package runs
`kanna-orm -destination <dir>`, which writes the implementation into `repository_gen.go` there: a struct named after the
interface when generated into a separate package, its constructor, and the `DBTX` it runs on, named as sqlc names it.
`//kanna:repository` on anything but an interface, and `//kanna:conflict` or `//kanna:lock` on anything but a method of
an interface, stop generation, as nothing reads them.

### Same-package implementations

Use `-destination .` to keep the interface and its implementation in the same package. The interface must be exported;
`Category` generates an unexported `category`, a `newCategory(db DBTX, opts ...option.Option) category` constructor,
and value receiver methods. It also generates `NewCategory(db DBTX, opts ...option.Option) Category` unless you
already declare a function `NewCategory` yourself, which can call `newCategory`.
The scope types may be in the package too: see [One package](#one-package).

Declare `Bind(db DBTX) Category`, in either package, and kanna-orm writes it, so that the repository can run on a
transaction. The argument may instead be a concrete database or transaction type implementing DBTX, or a type exposing a
parameterless `DBTX()` method that returns one, such as `Bind(tx tx.Tx) Category`. Bind returns a new implementation
using that connection and leaves the original unchanged. The argument, or what its `DBTX()` returns, must implement the
`DBTX` of the driver `-driver` names, and the result must be the repository's own interface. kanna-orm writes no `Bind`
when you have written one yourself.

The `DBTX` of every package of repositories is the one of the runtime, `type DBTX = kpgx.DBTX` of
`github.com/go-kanna/orm/pgx`, or `kpgx.CopyDBTX` where kanna-orm writes a [Copy](#copying-many-rows), or
`stdlib.DBTX` of `github.com/go-kanna/orm/stdlib` on `database/sql`. So one value serves them all, and a dependency
injector provides it once, as kanna-di does by a provider in the container's package:

```go
import "github.com/go-kanna/orm/pgx"

func provideDBTX(reader db.Reader) pgx.DBTX { return reader }
```

A repository that runs on another is given it by a provider of its own, written by hand, as
`func provideCategory(w db.Writer) Category { return NewCategory(w) }`.

Write methods of your own on the generated type. A method without a recognized verb or SQL query, other than `Bind`, is
yours to write there; generation stops while it is missing. When you implement a method with a recognized verb or SQL
query, kanna-orm generates its database operation under an unexported name: a hand-written `Upsert` can check its row
before calling `r.upsert`. kanna-orm writes the helper only when a method written on the type calls it on its receiver,
as `r.upsert`, so a method running SQL of its own needs none. The helper has the same signature and follows the
interface method's `//kanna:conflict` and `//kanna:lock` directives. Its name must not already be declared. Queries
belong in the destination's `.sql` files, including when the destination also declares the interface.

```go
//go:generate go tool kanna-orm -destination .

//kanna:repository
type Category interface {
	Bind(db DBTX) Category
	Upsert(ctx context.Context, c model.Category) error
}

func (r category) Upsert(ctx context.Context, c model.Category) error {
	if c.Name == "" {
		return errors.New("a category needs a name")
	}
	return r.upsert(ctx, c)
}
```

### Mapping errors

Write a `mapError` method on the implementation, and the error each generated method returns goes through it: the
error of a statement, of a scan, of a relation loaded, and `orm.ErrNotFound`, as wrapped with what the method was
doing. The [PostgreSQL](example/postgres) and [MySQL](example/mysql) examples turn the missing order `Get` reads into
`model.ErrOrderNotFound` in [postgres/errors.go](example/postgres/postgres/errors.go), next to the generated
implementation, so that the code using the repository sees no error of the database:

```go
func (Order) mapError(op orderOperation, err error) error {
    if op == orderOperationGet && errors.Is(err, orm.ErrNotFound) {
        return model.ErrOrderNotFound
    }
    return err
}
```

- The generator finds the method when it runs, and writes `return ..., r.mapError(orderOperationGet, err)` where a
  method returns an error. A method returning nil calls nothing.
- It takes an error, or the operation the error comes from and the error, and returns one, or generation stops. Its
  receiver is the implementation, a value or a pointer. For a repository implemented in its own package, that is the
  unexported type, as `func (*category) mapError`.
- Taking the operation, as `mapError(op orderOperation, err error) error`, it has kanna-orm write `orderOperation`, a
  string type named after the repository, and a constant of it for each method of the interface but `Bind`, holding
  the method's name: `orderOperationGet`, `orderOperationInsert`. An error of a relation the method loads, or of the
  row it reads back after a write, comes with the method's own operation. Taking the error alone, it has kanna-orm
  write neither, and maps the errors of every method alike.
- Each repository has its own. One without it returns the errors as they are. The names kanna-orm writes for one
  repository, declared by hand or written for another as well, stop generation.
- A method written by hand calls it itself, with its own operation if mapError takes one, as
  `r.mapError(orderOperationTag, err)`. The helper generated for it, as `r.upsert`, returns its errors as they are, for
  the method to map once.
- Return an error mapError does not know unchanged, to keep its chain.

The operation tells apart what one error means to each method. A foreign key violated is a missing customer to
`Insert`, and lines still there to `Delete`:

```go
func (Order) mapError(op orderOperation, err error) error {
    var pg *pgconn.PgError
    if errors.As(err, &pg) && pg.Code == "23503" { // foreign_key_violation
        if op == orderOperationDelete {
            return model.ErrOrderHasLines
        }
        return model.ErrCustomerNotFound
    }
    return err
}
```

### Separate-package implementations

With a separate destination such as `-destination ../postgres`, kanna-orm generates an exported struct with the
interface's name, `newOrder` and `NewOrder` constructors, and value receiver methods. All else goes as in the same
package: `Bind`, the helper of a method written by hand, and a `NewOrder` of your own.
As the interface's package cannot name the destination's `DBTX`, `Bind` takes a transaction exposing `DBTX()`, as
`Bind(tx tx.Tx) Order`, or the runtime's `DBTX` itself, of `orm/pgx` or `orm/stdlib`, or `CopyDBTX` of `orm/pgx` where
kanna-orm writes a [Copy](#copying-many-rows).

```go
// DBTX is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx.
type DBTX = kpgx.DBTX

func NewOrder(db DBTX, opts ...option.Option) Order
```

A transaction is begun and committed as it would be without kanna-orm: give the constructor the `pgx.Tx`, or the
`*sql.Tx`.

### One package

The models, their scope types, the repositories, and their implementations can all be one package. This document
shows a package for each, and one package changes no more than what is listed below.

```go
package store

//go:generate go tool kanna-orm -destination .

//kanna:scopes model-package=.

type Order struct {
	ID     int64
	Status string
}

//kanna:repository
type OrderRepository interface {
	List(ctx context.Context, s ...OrderScope) ([]Order, error)
	UpdateStatus(ctx context.Context, id int64, c OrderChangeSet) error
}
```

- A scope type `//kanna:scopes` implies of a model of its own package is named after the model and `Scope`, as
  `OrderScope`. The function returning it is named after the table as ever, `Orders()`, and its change set after the
  scope type but for its `Scope`, `OrderChangeSet` and `OrderChanges()`.
- One run writes both `scopes_gen.go` and `repository_gen.go`, each avoiding what the other declares.
- One `.sql` file may hold the package's `-- scope:` and `-- query:` statements both.
- A name kanna-orm writes that code written by hand sees, as `DBTX`, `newOrderRepository`, `Orders`, or
  `OrderChangeSet`, stops generation when the package declares it already, but for a function `NewOrderRepository` of
  your own, which replaces the generated one. The helpers it writes for itself start with `k` and take the first name
  free.

### Methods

What a method does is told by the verb its name starts with. What follows the verb is yours to choose for a method
reading or writing rows, while a method reading a column or an aggregate names its field there, as `ListNames` and
`SumTotal`. Its parameters tell the rest: a parameter of a model type is a row to write, the scopes say how to read, and
any other parameter is named after a field of the model, as `customerID` after `CustomerID`, and the rows must equal it
in the field's column. The column's name, which a tag may give, as `orm:"note"` of `Memo`, does not name a parameter:
`memo` does.

| Verb     | Declared as                                                                            | Does                                                                                  |
|----------|----------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------|
| `Get`    | `Get(ctx, id model.OrderID, s ...scopes.Order) (model.Order, error)`                   | reads the row whose key the parameters give, or returns `orm.ErrNotFound`             |
| `Find`   | `Find(ctx, id model.OrderID, s ...scopes.Order) (*model.Order, error)`                 | reads the row whose key the parameters give, or returns `nil, nil`                    |
| `First`  | `First(ctx, s ...scopes.Order) (model.Order, error)`                                   | reads the first row the scopes match, or returns `orm.ErrNotFound`                    |
| `List`   | `List(ctx, s ...scopes.Order) ([]model.Order, error)`                                  | reads every row the scopes match                                                      |
| `Count`  | `Count(ctx, s ...scopes.Order) (int64, error)`                                         | counts the rows the scopes match                                                      |
| `Exists` | `Exists(ctx, s ...scopes.Order) (bool, error)`                                         | reports whether any row matches                                                       |
| `Insert` | `Insert(ctx, o model.Order) (model.Order, error)`, or with `error` alone               | writes a row, or [many](#writing-many-rows)                                           |
| `Update` | `Update(ctx, o model.Order) error`, or with a [change set](#updating-selected-columns) | writes every column but the key, or returns `orm.ErrNotFound` when no row has the key |
| `Touch`  | `Touch(ctx, id model.OrderID) error`, or [with scopes](#touching-rows)                 | writes the [update time](#creation-and-update-times) alone, by the clock              |
| `Upsert` | `Upsert(ctx, o model.Order) error`, or with a [change set](#writing-a-change-set-over-the-row-met) | inserts, or updates the row with the same key, or [another unique key](#upserting)    |
| `Delete` | `Delete(ctx, id model.OrderID) error`, or [with scopes](#selecting-rows-to-delete)     | deletes the row with the key, or the rows the scopes match                            |
| `Copy`   | `Copy(ctx, rows []model.Order) (int64, error)`                                         | writes the rows by [COPY](#copying-many-rows) through pgx, and returns how many       |
| `Sum`    | `SumTotal(ctx, s ...scopes.Order) (int64, error)`                                      | adds up a column of the rows the scopes match: see [aggregates](#reading-aggregates)  |
| `Min`    | `MinPlacedAt(ctx, s ...scopes.Order) (time.Time, error)`                               | reads the least value of a column of the rows the scopes match                        |
| `Max`    | `MaxPlacedAt(ctx, s ...scopes.Order) (time.Time, error)`                               | reads the greatest value of a column of the rows the scopes match                     |
| `Avg`    | `AvgTotal(ctx, s ...scopes.Order) (float64, error)`                                    | reads the average of a column of the rows the scopes match                            |

`Get` requires the row to exist and returns `orm.ErrNotFound` when it does not. `Find` returns a pointer and treats
absence as `nil, nil`; database errors still return `nil, err`. Both require explicit key parameters covering a primary
or unique key. Additional scopes can filter the row and load relations; a scope that keeps only some of the rows, by a
`Limit`, an `Offset`, `After`, or `Before`, is an error for them, as it could leave out the very row the key names. A
key parameter takes the value, not a pointer: nil would compare by `= NULL`, which matches no row, so generation stops
at one, and the scope's `IsNull` narrows to the rows whose column is NULL. `First` selects a row from a potentially
larger set instead: the first in the scope's order, or by the primary key when the scope has none. Given `After`, it
reads the row right after one in the scope's order, which the scope needs then, as a page does. `Before` is an error for
it: the page before a row is read from its end, so the row read would be the one right before, not the first of that
page. Hand-written SQL may implement a `Find...` returning a pointer, to a result struct or to a scalar, which returns
`nil, nil` for no row as a generated `Find` does; a scalar one reads a column that is never NULL, as its nil means no
row.

```go
// customerID is customer_id: the orders of one customer.
ListByCustomer(ctx context.Context, customerID model.CustomerID, s ...scopes.Order) ([]model.Order, error)

// number is a unique key of orders, so Get reads one row by it.
GetByNumber(ctx context.Context, number model.OrderNumber) (model.Order, error)

// The key of order_lines is (order_id, sku), so Get takes both.
GetLine(ctx context.Context, orderID model.OrderID, sku model.SKU) (model.OrderLine, error)
```

- The table is the one whose scope type holds the method's model, so one interface may read and write every table of
  an aggregate.
- Several scopes hold together, as if chained.
- `Get` stops generation unless its parameters give the primary key or a unique key, every column of it.
- `Count`, `Exists`, and the [aggregates](#reading-aggregates) use their conditions only; a scope with an order, a
  limit, or a relation to load makes them return an error rather than ignore it.
- A `Delete` given scopes but no condition returns an error rather than delete every row.
- A `Delete` returning `error` alone returns `orm.ErrNotFound` when it deletes no row, as `Update` does; one returning
  `(int64, error)` returns the number of rows it deleted, none among them.
- A [hand-written query](#hand-written-sql) of the method's name takes the place of its verb; a method with neither
  stops generation.
- A method you write yourself on the generated struct stays in your file. Of a recognized verb or named query, it
  also gets a private helper when it calls one on its receiver, in either package.

A method tells its model by its result, its row, or its scopes. One that takes nothing but keys, as `Delete(ctx, id)`,
reads or writes the table the repository is named after: `Order` reaches `orders`, the table of the model `Order`, as
`//kanna:scopes` names it, and so does `OrderRepository`, whose `Repository` is left out. Name a repository in the
singular, as its model: a plural, as `Orders`, is made `orderses`. The type of a key tells nothing, so renaming a field
or adding a model never moves a method to another table. A repository named otherwise names its table with `table=`,
which wins over its name, and one that names neither stops generation at such a method:

```go
//kanna:repository table=todos
type Task interface {
	Get(ctx context.Context, id uuid.UUID) (model.Todo, error) // todos, by its result
	Delete(ctx context.Context, id uuid.UUID) error            // todos, by table=
}
```

The table of the name or of `table=` is that of the methods that tell none, and of no other: a method that tells its
own model reads or writes its table, so an interface of an aggregate still reaches each of its tables.

`orm.ErrNotFound` comes wrapped with what was not found, so test for it with `errors.Is`, or turn it into an error of
your application in [`mapError`](#mapping-errors).

### Keyed by a uuid

On Go 1.27 or later, a `uuid` column is held by the `uuid.UUID` of the standard library, which pgx and database/sql
read and write as it is; before, by a string, or by a type of your own with `Scan` and `Value` methods. A table keyed by
one is declared as any other:

```go
type Todo struct {
	ID    uuid.UUID
	Title string
}

//kanna:repository
type TodoRepository interface {
	Get(ctx context.Context, id uuid.UUID) (model.Todo, error)
	List(ctx context.Context, s ...scopes.Todo) ([]model.Todo, error)
	Upsert(ctx context.Context, t model.Todo) error
	Delete(ctx context.Context, id uuid.UUID) error
}
```

- A method that takes nothing but a uuid tells no table: `Delete` reaches `todos`, which `TodoRepository` is named
  after, while `Get` tells its own by its result.
- A uuid has no order to compare by, so its column gets `Eq`, `Ne`, `In`, `NotIn`, `Asc`, and `Desc`, but neither `Lt`
  nor `Between`. A page still continues from a row by it, and a cursor holds it as its text.
- A column defaulting to `uuidv7()` is not automatically treated as assigned. Without `orm:",default"`, an insert
  writes the model's ID, the zero UUID included. Either give each row its ID in Go with `uuid.NewV7()`, or tag the
  field with `orm:",default"` to use the database's default.
- MySQL has no `uuid` type, and kanna-orm does not take `uuid.UUID`, or a type declared of it, for a `binary(16)` or a
  `char(36)`: hold one in a `[]byte`, a string, or a type of your own with `Scan` and `Value` methods. A value type of
  kanna-value is checked by the value it holds, so one holding a `uuid.UUID` is not taken for these columns either.

A type declared of `uuid.UUID` holds a uuid as well, so that the key of one table is not passed for that of another:

```go
type TodoID uuid.UUID

type Todo struct {
	ID    TodoID
	Title string
}

//kanna:repository
type TodoRepository interface {
	Get(ctx context.Context, id model.TodoID) (model.Todo, error)
}
```

Go does not tell a type declared of `uuid.UUID` from one declared of `[16]byte`, so kanna-orm takes either. database/sql
reads a uuid into such a type from Go 1.27 on, so the module of the scope types must declare `go 1.27` or later in
`go.mod`, or generation stops. `uuid.UUID` itself exists from Go 1.27 on only, so it needs no such check. A cursor holds
such a key as the text of a uuid, as it holds a `uuid.UUID`.

[go-kanna/showcase](https://github.com/go-kanna/showcase) is a to-do service whose to-dos are keyed by `uuid.UUID`.

### Scalar relations

A relation can load one column of related rows directly into a slice, including a slice of enums. Name the related
table; no intermediate row model or scope type is needed:

```go
type Device struct {
    ID     uuid.UUID
    Topics []Topic `orm:"relation=device_topics"` // device_topics.topic, by device_topics.device_id
}
```

The column is the one whose plural the field's name is, as for [reading the values of a
column](#reading-the-values-of-a-column): `Topics` of `topic`, `Categories` of `category`, and `TopicIDs` of `topic_id`.
The rows are related by the foreign key of the model's name and `_id`, as for a has many: `device_topics.device_id` to
`devices`. A column of another name is named with `column=`, and a foreign key of other columns with `fk=`, whose
columns go on after it: `orm:"relation=device_topics,column=name,fk=tenant_id,device_id"`. Generation checks the
column's type and nullability against the slice element type, and stops when two columns have the plural, as `leaf` and
`leave` do, until `column=` names one.

A foreign key of more than one column, as from `device_topics (tenant_id, device_id)` to `devices (tenant_id, id)`,
matches and groups by the whole pair. The parent model must hold both referenced columns. Related values are loaded in
batches using tuple conditions, so rows with the same ID in different tenants remain separate. A parent with a nil
pointer in any key component has no related values loaded.

The generated device scope has `WithTopics()`. A read loads this relation only when its scope asks for it, in
batches, ordered by the selected column. Each enum value goes through its scanner. An explicitly loaded relation with
no rows is an empty slice; an unrequested relation remains nil. Parent inserts and updates leave the relation alone,
and a hand-written query reading the model need not return a column for it. Writing the related rows is yours to do.

```go
device, err := devices.GetByToken(ctx, platform, token, scopes.Devices().WithTopics())
```

### Writing

A row is written as the model holds it. A column the database assigns unless given, as a serial, an identity
`BY DEFAULT`, or an AUTO_INCREMENT one, is left to the database when the model's value is zero, and written when it is
not, as gorm and Rails do: an id is never 0, so 0 reads as none. So a fixture can give its ids, and an `Upsert` can meet
its row by such a key, inserting a row of no id and updating the one of its id. On PostgreSQL, writing a value does not
move the sequence, so a later row the database assigns may take the same one: give values the sequence will not reach,
or set it after. MySQL moves its AUTO_INCREMENT past a value written. A column the database always assigns, as an
identity `ALWAYS` or a generated one, is left out always, as is a field tagged `default`. Otherwise a zero value is
written as a zero value: a `false` for a column defaulting to `true` stays `false`. The times a row is created and
updated at are written by a clock: see [Creation and update times](#creation-and-update-times).

An `Insert` returning the model reads the row back as the database stored it; one returning `error` alone writes and is
done. A row goes by one of two constant statements, with the column and without, as its value says. The rows of a bulk
insert go by one statement, whether they give the column or not: on PostgreSQL, a row whose value is zero gets what the
database assigns the column by, which `describe` records, as `nextval('orders_id_seq'::regclass)`; on MySQL, it gives
`NULL`, which AUTO_INCREMENT assigns. MySQL reads a row back by `LAST_INSERT_ID()` when the database assigned its
primary key, and by its primary key, or an upsert's conflict key, otherwise. MySQL counts the rows an `Update` changes,
not those it finds, so when it changes none, kanna-orm asks whether the row is there before returning `orm.ErrNotFound`.

A method reading back what it writes, as an `Insert`, an `Update`, or an `Upsert` returning the model does, has written
it before it reads: when the read fails, as a time does on MySQL without [`parseTime=true`](#drivers-and-dialects), the
method returns the error, and the row stays written. Outside a transaction, a retry may write the row twice: run a write
that must not be repeated in a transaction, and roll it back on the error. On MySQL, a row written but not found by
its key when read back, as when a trigger changes the key, returns an error saying so, which is not `orm.ErrNotFound`,
since the row was written.

### Database defaults

Tag a field with `orm:",default"` to leave its column out of generated INSERT statements. A field with a different
column name can use `orm:"currency_code,default"`. The snapshot must record a database default or database assignment
for that column; otherwise generation fails, but for the [creation and update times](#creation-and-update-times),
which a trigger may assign. `default` and `nullzero` can be combined.

```go
type Product struct {
    SKU      SKU
    Name     string
    Price    int64
    Currency string `orm:",default"`
}
```

The [PostgreSQL](example/postgres) and [MySQL](example/mysql) examples define `products.currency` with `DEFAULT 'JPY'`.
Even when the model supplies `Currency: "USD"`, INSERT omits it and the stored value is `JPY`. The example reads it
back and checks this. The tag leaves the column out whatever the value, and a zero value without it is still written
as a zero value. It affects generated INSERT statements, including bulk inserts and upserts, and the columns a
[Copy](#copying-many-rows) writes, but not handwritten SQL.

On PostgreSQL, a method returning the model reads database-generated values with RETURNING. For example,
`ID uuid.UUID` tagged `orm:",default"` can use a `DEFAULT uuidv7()` primary key without assigning the ID in Go.
Without the tag, such a key is written as any other column is, the zero UUID of a model giving no ID included, and the
second such row conflicts with the first: only a serial, an identity, or an AUTO_INCREMENT column is left to the
database for a zero value, as [Writing](#writing) says.
A single row with every column omitted uses DEFAULT VALUES. Bulk writes still require at least one input column.

UPDATE keeps writing the model's values for tagged non-key fields. An upsert keeps a tagged column's stored value on a
conflict rather than reset it to its database default. The time a row is updated at is written on a conflict by the
clock, unless `default` leaves it to the database too: see [Creation and update times](#creation-and-update-times).

On MySQL, which has no RETURNING, a method returning the model reads its row back by AUTO_INCREMENT, by a primary
key it writes, or by an upsert's conflict key. When `default` leaves out the key that read needs, generation stops at a
method returning the model; one returning `error` alone reads nothing back and may leave it out. A bulk write returns
no model on MySQL.

### Creation and update times

The field named `CreatedAt` holds the time its row is created at, and the one named `UpdatedAt` the time it is last
updated at. kanna-orm writes both by a clock, so that the code above the repository sets neither. A model naming them
otherwise tags its fields `created` and `updated`; in a model with a field tagged one, the name of that kind is not
special:

```go
type Customer struct {
	ID        CustomerID
	Name      string
	CreatedAt time.Time // created_at
	UpdatedAt time.Time // updated_at
}

type Event struct {
	ID         int64
	Kind       string
	InsertedAt time.Time `orm:",created"`
	ModifiedAt time.Time `orm:",updated"`
}
```

| Write                                                          | Creation time    | Update time      |
|----------------------------------------------------------------|------------------|------------------|
| `Insert`, of one row or many, and an `Upsert` inserting a row  | the clock's time | the clock's time |
| `Update` of a row, or of a change set, by keys or by scopes    | kept             | the clock's time |
| `Upsert` meeting its row, of the row or of a change set        | kept             | the clock's time, but of an empty change set, which leaves the row as it was |
| [`Copy`](#copying-many-rows)                                   | the row's value  | the row's value  |

- Each is a `time.Time`. Another type, a pointer included, stops generation, and so do two fields of one kind in one
  model.
- The clock's time is written over whatever the model holds: a zero is not taken for one to fill in, and a value is
  not kept. A method reads the clock once, so the rows of one write get the same time.
- A change set has no setter of either, as `SetUpdatedAt`. An `Update` of a change set writes the update time with the
  columns it sets, and one of an empty change set is still an error.
- A `Copy` loads rows as they are, as an import keeping their original times does, so it writes the times the rows
  hold and reads no clock. Give new rows their times, or write them by an `Insert` of many rows.
- `insertonly` on either stops generation: an insert alone writes the creation time already, and every write of the
  row writes the update time. Remove the tag.
- `default` on either has the database assign it, by a DEFAULT or by a trigger: kanna-orm writes the column in no
  statement, an insert, an update, an upsert, or a copy, and a method returning the row reads it back, by RETURNING on
  PostgreSQL. As the snapshot records no triggers, it needs no default recorded there, as another `default` does; a
  column NOT NULL with neither a DEFAULT nor a trigger fails when a row is inserted.
- [Hand-written SQL](#hand-written-sql) writes what it says, these columns included.

The clock is `time.Now` unless the constructor is given an `orm.Clocker` by `option.WithClock`, as it is given hooks by
`option.WithHook`, and `Bind` keeps it. `Now` takes the context of the method writing, so that an application carrying
its clock in the context adapts it in one method:

```go
type clock struct{ now time.Time }

func (c clock) Now(context.Context) time.Time { return c.now }

repo := postgres.NewCustomer(db, option.WithClock(clock{now: time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)}))
```

A method you write on the implementation reads the same clock by `r.db.Now(ctx)`, as it runs statements through the
hooks on `r.db`, so that SQL of your own writing these columns gets the time a test gives:

```go
func (r Customer) ClearPhone(ctx context.Context, id model.CustomerID) error {
	_, err := r.db.Exec(ctx, "UPDATE customers SET phone = NULL, updated_at = $1 WHERE id = $2", r.db.Now(ctx), id)
	return err
}
```

kanna-orm writes the clock's time in UTC, whatever zone the clock tells it in, and `r.db.Now(ctx)` gives it in UTC too.
A `timestamptz` column keeps the same instant either way. A PostgreSQL `timestamp` column keeps no zone, and pgx writes
the time to it as its clock reads, so it keeps the time in UTC, as Rails keeps its `created_at`, and is read as UTC. A
MySQL `datetime` keeps no zone either: go-sql-driver/mysql writes and reads it in the zone its `loc` names, UTC unless
the DSN names another, so programs sharing a table give the same `loc`. A time read back is in the location the driver
reads it in, as pgx reads a `timestamptz` in the local zone and a `timestamp` in UTC, and go-sql-driver/mysql reads in
`loc`; `In` turns it into the zone you show it in.

A database may keep less of a time than Go does: PostgreSQL keeps microseconds, and MySQL the fractional digits of
its column, as `datetime(6)` keeps six and `datetime` none. A model a method returns holds the time as stored, read by
RETURNING on PostgreSQL and read back on MySQL, so it may differ from the time the clock gave. Compare them at the
precision the column keeps, or have a test's clock give times it keeps whole.

Both database examples write the times their customers sign up and change at by a clock they set.

### Upserting

`Upsert` meets an existing row by its primary key. To meet it by another unique key, name its columns with
`//kanna:conflict`:

```go
// package model
type User struct {
	ID        int64
	TenantID  int64
	Email     string
	Name      string
	CreatedAt time.Time
}

// package repository

//kanna:repository
type User interface {
	// UpsertByEmail meets the user of a tenant by their email address.
	//kanna:conflict tenant_id,email
	UpsertByEmail(ctx context.Context, u model.User) (model.User, error)
}
```

```sql
-- PostgreSQL
INSERT INTO users (tenant_id, email, name, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, email) DO UPDATE SET name = EXCLUDED.name
RETURNING id, tenant_id, email, name, created_at

-- MySQL, which then reads back by LAST_INSERT_ID() the row inserted or updated
INSERT INTO users (tenant_id, email, name, created_at)
VALUES (?, ?, ?, ?) AS new
ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(users.id), name = new.name
```

The columns must be the primary key or a unique key of the table, exactly, and none the database always assigns, as an
insert never gives those to meet a row by. The row met gets every column but those of the key, the primary key, those
tagged `insertonly`, and the [creation time](#creation-and-update-times); with none left, the key is set to itself, so
that the row is still read back.

A key may hold a column that may be NULL, but a row whose value there is NULL meets no other, as SQL takes no two NULLs
for equal: an upsert of such a row inserts one each time, and so does one of a `nullzero` field's zero value, which is
written as NULL. On PostgreSQL, a unique key declared `NULLS NOT DISTINCT` meets the row. Otherwise, to meet rows by a
key, hold its columns NOT NULL.

MySQL updates a row on a conflict over any unique key, not only the one an upsert meets its row by, so an upsert of a
table with other unique keys gets a warning, whether it names its key or meets the row by the primary key. The primary
key is among them when the insert writes it, as it writes the AUTO_INCREMENT `id` of a row whose `ID` is not zero; a key
holding every column of the one met is not, as it meets no other row. A key of a generated column is among them, as the
column's value comes of those the insert writes. On PostgreSQL, a conflict over another key returns a unique violation
and writes nothing.

```console
./user_repository.go:14:2: warning: UpsertByEmail: on MySQL, a conflict on (id) or (tenant_id, login) also updates the row
  users has unique keys other than (tenant_id, email)
  an insert writes id of a row whose ID is not zero
```

A field tagged `orm:",insertonly"` is written by an insert alone, as the channel a user signed up through: an `Update`,
and an upsert meeting its row, leave its column as it was, and a change set has no setter for it. With `default`, which
leaves it out of an insert, it would never be written, so the two together stop generation.

A field tagged `orm:",readonly"` holds a column the database writes on every write, such as a version a trigger counts
up. No insert, update, or upsert sends it, a change set has no setter for it, and a method returning the model reads it
back (by RETURNING on PostgreSQL). The snapshot records no triggers, so the tag states what it cannot, and kanna-orm
treats the column as it treats a generated one. Generation stops when the column is one the database assigns already,
when the tag adds `default` or `insertonly`, which `readonly` makes redundant, or when it is a [creation or update
time](#creation-and-update-times): tag a time the database writes `default` instead.

#### Writing a change set over the row met

An upsert taking a [change set](#updating-selected-columns) inserts its row when no row has the key, and writes the
change set alone over the row it meets:

```go
//kanna:conflict email
UpsertByEmail(ctx context.Context, c model.Customer, changes scopes.CustomerChangeSet) (model.Customer, error)
```

```go
changes := scopes.CustomerChanges().SetName(c.Name)
if c.Phone != nil {
	changes = changes.SetPhone(c.Phone)
}
stored, err := repo.UpsertByEmail(ctx, c, changes)
```

The examples' `Customer` repository signs customers up this way: one met gets their new name, and their phone only when
they give one. The row's values go to the insert alone, so the change set writes NULL or an empty string as it writes
any other value. An empty change set inserts the row when none has the key, and otherwise reads back the row met as it
was. A change set writing a column of the conflict key returns an error and writes nothing: change a key by an `Update`.
Only an upsert of one row takes a change set; generation stops at a bulk upsert taking one, whose values would go over
every row it meets.

The statement is the same whatever the change set holds: a column it can write takes the change set's value when it
gives one, and keeps the stored value otherwise. The [update time](#creation-and-update-times) takes the clock's time
when the change set gives any value, and so is kept by an empty one.

```sql
-- PostgreSQL
ON CONFLICT (email) DO UPDATE SET name = CASE WHEN $5 THEN $6 ELSE customers.name END, phone = CASE WHEN $7 THEN $8 ELSE customers.phone END

-- MySQL
ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(customers.id), name = IF(?, ?, customers.name), phone = IF(?, ?, customers.phone)
```

A column that has [`Add`](#updating-selected-columns) takes a second branch, which adds the change set's value to the
stored one when the change set adds it. Were customers to have a `Visits int32` field, `AddVisits(1)` would count a
visit of the customer met, and the row inserted when none is met would get its own value:

```sql
-- PostgreSQL
visits = CASE WHEN $9 THEN $10 WHEN $11 THEN customers.visits + $12 ELSE customers.visits END

-- MySQL
visits = IF(?, ?, IF(?, customers.visits + ?, customers.visits))
```

A number added to the stored value, as `visits = visits + 1`, is thus written by `Add`; any other value computed from
the row met is [SQL written by hand](#hand-written-sql).

### Writing many rows

An `Insert` or an `Upsert` given a slice writes every row in it:

```go
InsertLines(ctx context.Context, lines []model.OrderLine) error
```

```sql
-- PostgreSQL: one statement for any number of rows
INSERT INTO order_lines (order_id, sku, quantity)
SELECT * FROM unnest($1::uuid[], $2::text[], $3::int[])
```

- On PostgreSQL, a result of `([]model.OrderLine, error)` reads the rows back with `RETURNING`, in no guaranteed
  order, as those of an `Update` or a `Delete` of many rows: to tell which row is which, match them by a key you hold,
  or insert the rows one by one.
- On MySQL, the rows go in `VALUES (?, ?, ?), (?, ?, ?), ...`, split into several statements when they need more
  placeholders than one can take. Give the constructor a transaction to have them stand or fall together.
- MySQL has no `RETURNING`, so there a result other than `error` alone stops generation.
- PostgreSQL rejects an upsert in which two rows have the same key.
- On PostgreSQL, each array is cast to the type of its column. For a column of a type outside `pg_catalog`, such as an
  enum type, the array goes as `text[]` first, as in `$1::text[]::public.order_status[]`, so pgx need not know the type.
- `unnest` flattens an array of arrays, so on PostgreSQL a table with an array column stops generation; write its rows
  one by one, or [copy them](#copying-many-rows).

### Copying many rows

A `Copy` writes the rows of a slice by COPY of PostgreSQL, through `CopyFrom` of pgx, and returns how many it wrote:

```go
CopyEvents(ctx context.Context, events []model.Event) (int64, error)
```

```sql
-- what the hooks see
COPY "events" ("kind", "tags", "note") FROM STDIN
```

COPY has no `RETURNING` and no `ON CONFLICT`, and a table with row-level security refuses it, so it is a verb of its
own, which an `Insert` of many rows never turns into.

- It writes the columns an `Insert` of many rows writes, leaving out those tagged `default` and those the database
  always assigns. It writes the [creation and update times](#creation-and-update-times) as the rows hold them, and
  reads no clock. One COPY writes a column of every row or of none, so a column the database assigns unless given, as
  a serial, is written when every row gives it a value and left to the database when none does, and rows of both
  kinds return an error and write nothing: copy them apart.
- An array, which `unnest` cannot write, is copied as any other value.
- Of no rows, it runs nothing and returns 0.
- Its error goes through [`mapError`](#mapping-errors), with the method's operation, and the hooks see the COPY
  statement with no `Args`.
- pgx writes each value in the binary format. A string of a type pgx does not know goes as its text, which is also
  the binary format of an enum type, so an enum held as a string needs no type registered. A column of another type
  pgx does not know, whose binary format differs from its text, needs the type registered on the connection first, by
  `Conn.LoadType` and `pgtype.Map.RegisterType`.
- It needs pgx: on MySQL, or with `-driver database/sql`, generation stops at it. Write the rows by an `Insert` of many
  rows there.

A package where kanna-orm writes a `Copy`, or the helper a `Copy` written by hand calls, declares its `DBTX` as
`kpgx.CopyDBTX`, the runtime's `DBTX` with `CopyFrom`, which `*pgxpool.Pool`, `*pgx.Conn`, and `pgx.Tx` have, so that a
connection that cannot copy does not compile. A method named `Copy...` that you write and that calls no helper, as a
`CopyOrder` duplicating an order, leaves `DBTX` as it is; to call `r.db.CopyFrom` yourself, declare a `Copy` kanna-orm
writes. The `Bind` of each repository in such a package takes a `CopyDBTX`, as `Bind(db kpgx.CopyDBTX) Order`, or a
transaction whose `DBTX()` returns one, and generation stops at a `Bind` taking what cannot copy. A dependency injector
provides `CopyDBTX` besides `DBTX`:

```go
func provideCopyDBTX(pool *pgxpool.Pool) pgx.CopyDBTX { return pool }
```

The [PostgreSQL](example/postgres) example copies the approvals of its orders this way, and its After prints the COPY.

### Updating selected columns

Each scope generates a typed change set, named after the scope type but for a `Scope` it ends in, so that `OrderScope`
has `OrderChangeSet` too; two scope types of a package whose change sets would share the name stop generation.
`CustomerChanges()` returns an empty `CustomerChangeSet`; setters add only the columns to write. Setters use the model
field's Go type, so a nullable `Phone *string` accepts `nil` to store SQL NULL:

```go
changes := scopes.CustomerChanges().
    SetName("Ann Lee").
    SetPhone(nil)
```

A column that is a number in Go and in the database, and never NULL, has `Add` besides `Set`: its field is of an integer
or floating-point type, or a type based on one, and its column of an integer, decimal, or floating-point type. `Add`
adds to the stored value within the statement, so that no update made between a read and the write is lost:

```go
changes := scopes.OrderLineChanges().AddQuantity(1)
```

```sql
-- PostgreSQL
UPDATE "order_lines" SET "quantity" = "quantity" + $1 WHERE ...

-- MySQL
UPDATE `order_lines` SET `quantity` = `quantity` + ? WHERE ...
```

A nullable column has no `Add`, as NULL plus a number stays NULL, and neither has a number a `Valuer` stores as text.

Not calling a setter leaves that column unchanged. Zero values are written, rather than skipped. A later `Set` or `Add`
of the same column replaces the earlier one. Change sets are immutable: a derived set leaves the original unchanged.
Fields tagged `nullzero` retain their usual conversion of the empty string to SQL NULL. Primary keys, database-assigned
columns, columns tagged `insertonly` or `readonly`, the [creation and update times](#creation-and-update-times), and
relations have no setters; an `Update` of a change set writes the update time by the clock. An `Update` of an empty
change set returns `orm.ErrEmptyChangeSet`, which a handler can test for with `errors.Is` to reject a request that
changes nothing; an [upsert](#writing-a-change-set-over-the-row-met) of one is not an error.

Declare the unique key separately from the assigned values and add ordinary condition scopes:

```go
UpdatePhone(ctx context.Context, id model.CustomerID,
    c scopes.CustomerChangeSet, s ...scopes.Customer) (model.Customer, error)

updated, err := repo.UpdatePhone(ctx, customerID,
    scopes.CustomerChanges().SetPhone(new("050-1234-5678")),
    scopes.Customers().PhoneIsNull())
```

An Update of a change set selects the rows it writes by keys, scopes, or both; one taking neither stops generation
rather than write every row. On PostgreSQL, a model result generates `UPDATE ... RETURNING` and reads the stored row in
the same statement. A failed condition or missing key returns `orm.ErrNotFound`. Composite and alternate unique keys are
supported. Returning only `error` updates the row without reading it back. A whole-model `Update` takes condition scopes
too, and may return the updated model. Returned models contain mapped table columns; related values are not loaded by an
update.

For multiple rows, declare `(int64, error)` for the affected count or `([]model.Customer, error)` for the returned rows.
Returned rows have no guaranteed order. MySQL supports change sets with `error` or an affected count; its count is the
number of changed rows. A single-row update that matches unchanged values still succeeds. A declaration returning
updated models stops generation on MySQL, which has no UPDATE RETURNING; no separate readback is performed.

Both database examples update a customer's phone this way, and clear it with `SetPhone(nil)`.

An Update of a change set taking scopes, and no unique key as parameters, selects its rows as a [`Delete` taking
scopes](#selecting-rows-to-delete) does: its conditions, order, and page say which rows. Parameters naming columns that
are no unique key, as `customerID` of `UpdateByCustomer(ctx, customerID, c, s...)`, are conditions, as those of a
`Delete` are.

```go
Update(ctx context.Context, c scopes.OrderChangeSet, s ...scopes.Order) (int64, error)

n, err := repo.Update(ctx, scopes.OrderChanges().SetMemo("Follow up"), scopes.Orders().
    Open().
    PlacedAtAsc().
    IDAsc().
    Limit(batch))
```

Without pagination, the method updates all matching rows. With `Limit` or `Offset`, it selects the target keys in a
subquery and updates those rows in one statement, as a paged `Delete` does, on PostgreSQL and MySQL. Its rules are those
of the `Delete`: a WHERE condition is required even when a limit is supplied; a page is chosen as a read of the same
scope chooses it, an order ending in the key and a page of no order holding whichever rows the database reads first;
`After` and `Before` select pages too; an order without a limit, offset, or cursor is rejected rather than ignored;
relation-loading scopes are rejected; and a table with neither a primary key nor a non-null unique key cannot be paged.
`Limit(0)` and no matching rows return a count of zero, or an empty slice, without `orm.ErrNotFound`. Ordering chooses
which rows to update; the returned rows have no guaranteed order. Both database examples write a memo on the two oldest
open orders with a limit.

An Update taking a unique key, or a whole row, writes the one row its key names, so its scopes take conditions alone:
ordering, paging, or loading relations in one is an error, as paging a `Get` is.

`//kanna:lock` on an Update of a change set taking scopes and no unique key locks its rows in that subquery, which the
statement then has whether its scope pages them or not, so that workers updating batches pass over the rows another
holds. See [Locking rows](#locking-rows).

### Touching rows

A `Touch` writes the update time of its rows by the clock and no other column, to mark a row changed when what changed
is in another table. It selects its rows as an `Update` of a change set does: by a unique key, by scopes, or by both.
Of one row it returns `error` alone, and `orm.ErrNotFound` when no row has the key. Of the rows its scopes select, a
page of them too, it returns `(int64, error)`:

```go
Touch(ctx context.Context, id model.OrderID) error
TouchOpen(ctx context.Context, s ...scopes.Order) (int64, error)
```

```sql
UPDATE "orders" SET "updated_at" = $1 WHERE ...
```

A `Touch` of a model with no [update time](#creation-and-update-times) kanna-orm writes, such as one whose update time
is tagged `default`, stops generation, since it would write nothing. An `Update` of an empty change set still returns
`orm.ErrEmptyChangeSet` and leaves its rows alone.

### Selecting rows to delete

A `Delete` taking scopes returns the number of rows it deleted. Its conditions, order, and page say which rows:

```go
Delete(ctx context.Context, s ...scopes.Order) (int64, error)

n, err := repo.Delete(ctx, scopes.Orders().
    Open().
    PlacedAtLt(before).
    PlacedAtAsc().
    IDAsc().
    Limit(batch))
```

Without pagination, the method deletes all matching rows. With `Limit` or `Offset`, it selects the target keys in a
subquery and deletes those rows in one statement. Composite primary keys and non-null unique keys are supported, on
PostgreSQL and MySQL. A table without either kind of key can still use an ordinary condition-based delete, but cannot
use paged deletion.

A WHERE condition is required even when a limit is supplied. A page of rows to delete is chosen as a read of the same
scope chooses it: an order ends in the key, so that rows equal in it are chosen alike, and a page of no order holds
whichever rows the database reads first, as in SQL. A page continued by `After` or `Before` selects them too. An order
without a limit, offset, or cursor is rejected rather than ignored. Relation-loading scopes are rejected. `Limit(0)` and
no matching rows both return a count of zero without `orm.ErrNotFound`.

A `Delete(ctx, id) error` by a key returns `orm.ErrNotFound` when no row has it; the shape of the method, not its name,
tells the two apart. Both database examples delete the oldest matching order with a limit.

On PostgreSQL, a slice result generates `DELETE … RETURNING` to return the deleted rows in the same statement:

```go
DeleteReturning(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
DeleteSummaries(ctx context.Context, s ...scopes.Order) ([]DeletedOrder, error)

type DeletedOrder struct {
    ID         model.OrderID
    CustomerID model.CustomerID
}

deleted, err := repo.DeleteSummaries(ctx, scopes.Orders().
    Open().PlacedAtLt(before).PlacedAtAsc().IDAsc().Limit(batch))
```

The result can be the original model or a dedicated projection type with the same field mapping and type rules as read
projections. Only the result's columns appear in `RETURNING`; relations are not loaded. Key parameters can be combined
with scopes. The condition and pagination requirements above still apply. No matches and `Limit(0)` return an empty,
non-nil slice without `orm.ErrNotFound`. Ordering chooses which rows to delete; the returned rows have no guaranteed
order. MySQL has no `DELETE RETURNING`, so a slice result stops generation there.

### Locking rows

`//kanna:lock` has a `Get`, a `Find`, a `First`, or a `List` lock the rows it reads until the transaction ends, so that
what was read still holds when it is written back:

```go
//kanna:repository
type Order interface {
	//kanna:lock update
	GetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)

	// FirstToShip passes over the orders other workers hold.
	//kanna:lock update skip-locked
	FirstToShip(ctx context.Context, s ...scopes.Order) (model.Order, error)
}
```

```go
tx, err := pool.Begin(ctx)
if err != nil {
	return err
}
defer tx.Rollback(ctx)

orders := postgres.NewOrder(tx)
o, err := orders.GetForUpdate(ctx, id)
if err != nil {
	return err
}
if o.Status != model.OrderStatusOpen {
	return ErrNotOpen
}
o.Status = model.OrderStatusCanceled
if err := orders.Update(ctx, o); err != nil {
	return err
}
return tx.Commit(ctx)
```

| Strength        | Clause              | PostgreSQL | MySQL |
|-----------------|---------------------|------------|-------|
| `update`        | `FOR UPDATE`        | yes        | yes   |
| `no-key-update` | `FOR NO KEY UPDATE` | yes        | no    |
| `share`         | `FOR SHARE`         | yes        | yes   |
| `key-share`     | `FOR KEY SHARE`     | yes        | no    |

A read waits for the rows other transactions hold. `nowait` after the strength has it return the database's error at
once instead, and `skip-locked` has it pass those rows over.

An [`Update` of a change set](#updating-selected-columns) taking scopes and no unique key, the one that can take a
page, takes `//kanna:lock` too. It then selects its rows by key in a subquery whether its scope pages them or not, and
the lock is on the subquery, so that the rows it waits for, fails on, or passes over are those it would write:

```go
// UpdateFree writes the orders no other worker holds.
//kanna:lock update skip-locked
UpdateFree(ctx context.Context, c scopes.OrderChangeSet, s ...scopes.Order) (int64, error)

n, err := orders.UpdateFree(ctx, scopes.OrderChanges().SetStatus(model.OrderStatusShipped),
    scopes.Orders().Open().PlacedAtAsc().IDAsc().Limit(batch))
```

Its lock needs no transaction: outside one, it holds for the statement, and inside one, until the transaction ends, as
that of any UPDATE does. An Update taking a unique key, or a table with neither a primary key nor a non-null unique key
to select the rows by, stops generation. So does MySQL, whose UPDATE may still read, and wait for, rows the subquery
passed over, as InnoDB locks the rows a statement scans.

- The lock belongs to the method, not to its scopes: they narrow, order, and page the rows as ever, and no caller can
  leave the lock out or change it.
- A lock of a read lasts as long as its transaction, so a read with one returns an error on anything else. It tells by
  whether what it runs on has a `Commit`, or wraps what has one: a `DBTX` of your own wrapping a transaction says so by
  an `Unwrap` returning it, as `Unwrap() pgx.DBTX` of `orm/pgx`, or `Unwrap() stdlib.DBTX` on `database/sql`, however
  many wrappers deep.
- A lock not given, by `nowait`, a timeout, or a deadlock, comes back as the database's error, wrapped, never as
  `orm.ErrNotFound`. Retrying is yours to do.
-  With `skip-locked`, `orm.ErrNotFound` from a `First` says that no free row matches, not that no row does. A `Get` or
  `Find` would say a held row is not there, so `skip-locked` on one stops generation; use `nowait`.
- The relations a scope loads are read without a lock.
- Only rows that exist are sure to be locked, so do not count on a lock to keep a row from being inserted.
- PostgreSQL also locks the rows an `Offset` skips. InnoDB locks the rows it scans, not those it returns, so a read with
  no index for its condition locks the whole table.
- `//kanna:lock` parted from such a method by a blank line, or on a method of an interface no `//kanna:repository`
  marks, gets a warning, as nothing reads it and the method is generated without the lock; one on anything but a method
  of an interface stops generation. One on a method a [hand-written query](#hand-written-sql) or your own code alone
  implements stops generation: write the lock there, and in a query taking scopes, mind
  [what it locks](#scopes-on-a-query). A method delegating to a generated helper keeps its lock on that helper.
-  A `Get`, a `Find`, a `First`, or a `List` whose name ends in a lock, such as `GetForUpdate` or `ListForShare`, gets a
  warning without `//kanna:lock`, and stops generation with one of another strength. A name in which more follows, such
  as `GetForUpdateForm`, says nothing of a lock.

### Hooks

Generated constructors accept optional repository options. `option.WithHook` takes a `hook.Hook`, a Before and an After
a repository calls around each statement it runs, with no logging or tracing library dependency. Either may be nil. To
log queries, give an After:

```go
logQuery := func(ctx context.Context, e hook.Execution) {
    fmt.Printf("%s (%s): %v\n", e.SQL, e.Duration, e.Err)
    // Use ctx for request metadata; choose whether to log e.Args.
}
repo := postgres.NewOrder(db, option.WithHook(hook.Hook{After: logQuery}))
```

A Before receives a `hook.Query`, with `SQL string` and `Args []any`, as the statement starts, and returns the context
the statement runs with: a tracer can start a span there. Return the context received, or one derived from it; its
cancellation and deadline apply to the statement. An After receives the context its own Before returned and a
`hook.Execution`, the `Query` with `Duration time.Duration` and `Err error`, after execution and result consumption, or
on a query, scan, or iteration error. Duration includes reading the results, not the Befores. Errors are observed before
`mapError`. Each executed statement is reported once, including generated CRUD, SQL from `.sql` files, and each
statement loading relations. A [Copy](#copying-many-rows) is seen as its COPY statement, as
`COPY "events" ("kind", "tags", "note") FROM STDIN`, with no `Args`. A condition or validation error that prevents SQL
from running calls no hook.

Hooks can be given more than once, and those given first enclose the others: each Before runs in the order given, with
the context the one before returned, and each After in the reverse order, with the context of its own Before. So a
tracing hook that starts a span in its Before and ends the span of its context in its After ends its own span, whatever
hooks come after it. `option.Join` gives options as one, in their order, for a dependency injector that holds one value
of a type. `Bind` keeps the hooks when switching to a transaction. Without an After, no time is measured.
Hooks can run concurrently and should be safe for concurrent use. Treat arguments as borrowed values: do not modify
them, and copy values you need to retain after the call. This works with PostgreSQL through pgx or `database/sql`, and
MySQL through `database/sql`. A method you write on the implementation goes through the hooks too when it runs its
statements on `r.db`, the one field of the implementation. Its methods are those of DBTX: `r.db.Exec`, `r.db.Query`, and
`r.db.QueryRow` on pgx, with `r.db.CopyFrom` in a package where kanna-orm writes a Copy, and `r.db.ExecContext`,
`r.db.QueryContext`, and `r.db.QueryRowContext` on `database/sql`. As `*sql.Rows` and `*sql.Row` are types nothing can
read through, the hooks see a query on `database/sql` done as `r.db.QueryContext` or `r.db.QueryRowContext` returns,
before its rows are read: its `Duration` and `Err` leave the reading out. The methods kanna-orm generates read their
rows through the hooks, on either driver. Both database examples give an After that prints each statement.

### Reading the values of a column

A `Get`, a `First`, a `Find`, or a `List` returning the values of a column rather than rows reads that column: the one
of the field its name says after the verb, as `GetName` reads `Name`, or, of a `List`, its plural, made as that of a
table's name, as `ListNames` reads `Name` and `ListCategories` reads `Category`, or, of a field ending in an acronym,
with an s, as `ListCustomerIDs` reads `CustomerID`. It returns a value of the field's type, a pointer to one of a
`Find`, or a slice of them of a `List`, as a projection does. The name says what it reads, and nothing more: which rows,
as ever, are told by its keys and scopes, so a name such as `ListNamesByPrice` stops generation. Conditions, ordering,
limits, offsets, cursors, and `//kanna:lock` work as for rows; a scope loading a relation is an error, as there is no
row to load it into. Duplicate values are preserved and no matching rows returns an empty slice.

```go
ListNames(ctx context.Context, s ...scopes.Product) ([]string, error)
```

```go
names, err := products.ListNames(ctx, scopes.Products().PriceLt(2000).PriceAsc())
```

Both database examples read the affordable product names this way.

A name may also end in its lock, which is taken off before the field is read, so `ListIDsForKeyShare` with
`//kanna:lock key-share` reads `ID` and locks its rows `FOR KEY SHARE`:

```go
//kanna:lock key-share
ListIDsForKeyShare(ctx context.Context, s ...scopes.Order) ([]model.OrderID, error)
```

### Reading a projection

A `Get...`, `Find...`, `First...`, or `List...` method may return a different named struct while taking the original
model's scopes. The result type selects the columns; it needs no scope of its own:

```go
type ProductSummary struct {
    Name  string
    Price int64
}

// Inside a //kanna:repository interface:
ListSummaries(ctx context.Context, s ...scopes.Product) ([]ProductSummary, error)
```

`ListSummaries(ctx, scopes.Products().SKUAsc().PriceLt(2000))` selects only `name, price` from `products`, while
filtering and ordering by the original scope. A field matches the field of the original model of its name, whose column
tag and `nullzero` handling it inherits; a field of another name names the column with a tag, as `orm:"note"`. Field
types must match the corresponding model field exactly. Missing fields, duplicate source columns, empty projections, and
relations in the result stop generation. Filters, ordering, paging, and locks remain available, and a read of no
row returns what it does of the model: `orm.ErrNotFound` of a `Get` or a `First`, and nil of a `Find`. A scope
requesting relation loading is rejected rather than silently ignored. A projection may hold
[aggregates](#reading-aggregates) of groups of rows; other expressions can still use hand-written SQL returning a DTO.

### Joining a projection

A projection reads the columns of a table its table refers to by a nested struct. A struct is an INNER JOIN, and a
pointer to one a LEFT JOIN, nil when no row joins:

```go
type MembershipSummary struct {
    UserID  uuid.UUID
    Role    Role
    User    struct{ DisplayName string }  // users, by user_id: INNER JOIN
    Inviter *struct{ DisplayName string } // users, by inviter_id: LEFT JOIN
}

GetSummary(ctx context.Context, userID uuid.UUID, s ...scopes.Membership) (MembershipSummary, error)
```

```sql
SELECT memberships.user_id, memberships.role, "user".display_name, inviter.id IS NOT NULL, inviter.display_name
FROM "memberships"
INNER JOIN users AS "user" ON "user".id = memberships.user_id
LEFT JOIN users AS inviter ON inviter.id = memberships.inviter_id
```

-  The table joined is the one the foreign key of the field's name and `_id` refers to, as for a [relation](#relations).
  A foreign key of other columns is named by a tag, as `orm:"fk=created_by"`, or `orm:"fk=tenant_id,customer_id"` for
  more than one.
-  The fields of a nested struct match the fields of the model of the table joined, by name or by a column tag, as those
  of the projection match the original model's. The table joined needs a scope type, which tells its model; its
  conditions and relations are not brought in.
-  A table goes by the name of its field in snake case, so one can be joined twice, or to itself, as
  `Parent *struct{ Name string }` of categories by `parent_id`. A name the table read or another field goes by already
  stops generation.
-  A LEFT JOIN tells a row joining from none by the column the foreign key refers to, so a column of the row that is
  NULL reads as nil or as the zero `nullzero` makes it, and the struct is still there.
-  The nested type is a struct of its own, not a model: a model is read by a [relation](#relations). A struct in a
  nested struct, a table that no foreign key of the table read refers to, and a join of a Delete are written by hand.
-  The scope's conditions read the table read alone, and its order and limit follow the joins, so rows an INNER JOIN
  leaves out are not counted, and `First` reads the first row of the join. The order is by columns of the table read. A
  projection joining a table takes no `//kanna:lock`: lock the rows by reading the model, or write the query by hand, as
  `FOR UPDATE OF` locks the rows of the tables meant.

Both database examples read the name of an order's customer into an order summary this way.

### Reading aggregates

A `Sum`, a `Min`, a `Max`, or an `Avg` reads the sum, the least value, the greatest value, or the average of the column
of the field its name says after the verb, as `SumTotal` reads the sum of `Total`. As for [the values of a
column](#reading-the-values-of-a-column), the name says what it reads, and nothing more: which rows, as ever, are told
by its keys and scopes, so a name such as `SumTotalByStatus` stops generation.

```go
SumTotal(ctx context.Context, s ...scopes.Order) (int64, error)
MaxPlacedAt(ctx context.Context, s ...scopes.Order) (*time.Time, error)
AvgTotal(ctx context.Context, customerID model.CustomerID) (float64, error)
```

```sql
SELECT coalesce(sum(total)::bigint, 0) FROM orders WHERE ...
```

-  `Sum` and `Avg` read a column of numbers, held by a field of an integer or a floating-point type, or of a type made
  of one; `Min` and `Max` read a column of numbers, strings, or times. A column of another kind stops generation. MySQL
  compares the values of an `ENUM` column as strings, not in the order the type declares them.
-  `Sum`, `Min`, and `Max` return a value of the field's type, or a pointer to one: of a field `*int64`, as a column
  that may be NULL has, an `int64` or an `*int64`. `Avg` returns a `float64` or a `float32`, or a pointer to one, as the
  average of integers is seldom one. Any other type stops generation. A sum too large for its type is an error when the
  method runs, not a value cut short.
-  When no row matches, or the column is NULL in every row matched, one returning a value returns 0 of a `Sum`, and
  `orm.ErrNotFound` of a `Min`, a `Max`, or an `Avg`, as a `Get` does; one returning a pointer returns nil, as a `Find`
  does.
-  Their scopes give their conditions alone, as those of `Count` do: a scope that orders, pages, or loads a relation
  makes them return an error.
-  On PostgreSQL, a sum of integers is cast to `bigint`, as the sum of `bigint`s is `numeric`, and an average to
  `double precision`. On MySQL, an average is cast to `DOUBLE`, as one of integers is otherwise a `DECIMAL` of four
  decimal places; a sum of integers stays a `DECIMAL`, which database/sql reads into the field's type, failing if it
  does not fit.

A projection read by a `List` may hold aggregates of groups of rows. A field named `Count`, an `int64`, holds the number
of the rows of a group, and one named `Sum`, `Min`, `Max`, or `Avg` followed by the name of a field of the model holds
that aggregate of its column, of a type its verb returns. The rows are grouped by the other fields, which name the
model's columns as those of any projection do:

```go
type StatusCount struct {
	Status model.OrderStatus
	Count  int64
}

ListStatusCounts(ctx context.Context, s ...scopes.Order) ([]StatusCount, error)
```

```sql
SELECT status, count(*) FROM orders WHERE ... GROUP BY status
```

-  A group has rows, but a column that may be NULL may be NULL in every row of one: a `Min`, a `Max`, or an `Avg` of it
  is a pointer, nil for such a group, and a `Sum` of it is 0, or nil of a pointer.
- The groups come in no order, as SQL returns them. The scopes give their conditions alone, as those of `Count` do.
-  A projection of aggregates is read by a `List`, groups by one field or more, joins no table, and takes no
  `//kanna:lock`; generation stops otherwise. A field of an aggregate takes no tag, and a model with a field named as
  one, as `Count`, stops generation, as the projection could read either.
- HAVING, an order by an aggregate, and other shapes are written in [SQL](#hand-written-sql) returning a DTO.

Both database examples count their orders by status this way.

## Hand-written SQL

What the methods cannot say goes into a `.sql` file. A query becomes the repository method of its name, and is written
where that method is implemented, in the package `-destination` names. A method whose name starts with a verb returns
what a generated one of the verb does, so that the verb says the same wherever it is read: a `Get` or a `First` one row
and `orm.ErrNotFound` for none, a `Find` a pointer and nil for none, a `List` a slice, a `Count` an `int64`, an
`Exists` a `bool`, a `Sum`, a `Min`, or a `Max` a value or a pointer, and an `Avg` a `float64` or a `float32`, or a
pointer to one, of the one column of its result, and an `Insert`, an `Update`, an `Upsert`, or a `Delete` error
alone, `(int64, error)` for the rows it affects, or the rows its `RETURNING` gives. Another stops generation; a name
that starts with no verb, as `MonthCounts`, returns what it likes. With `-destination .`, queries sit next to the
repository interface. A scope becomes a method of the scope type it names, and is written next to it. A `.sql` file with
neither a `-- query:` nor a `-- scope:` line, such as a migration, is none of kanna-orm's.

```text
repository/repository.go   //kanna:repository interfaces, MonthCounts among their methods
postgres/queries.sql       -- query: MonthCounts
scopes/order_scopes.sql    -- scope: Order.PlacedInMonth(at time.Time)
```

For `-destination .`, the layout is:

```text
repository/repository.go   //kanna:repository interfaces; //go:generate go tool kanna-orm -destination .
repository/queries.sql     -- query: MonthCounts
repository/repository_gen.go      generated private implementations and DBTX
scopes/order_scopes.sql    -- scope: Order.PlacedInMonth(at time.Time)
```

```sql
-- query: MonthCounts
SELECT date_trunc('month', placed_at) AS month, count(*) AS count
FROM orders
WHERE placed_at >= :since
GROUP BY 1
ORDER BY 1;
```

```go
MonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)

type MonthCount struct {
	Month time.Time
	Count int64
}
```

```sql
-- PlacedInMonth narrows to the orders placed in the month of at.
-- scope: Order.PlacedInMonth(at time.Time)
date_trunc('month', placed_at) = date_trunc('month', :at::timestamptz)
```

The generated code holds each as a literal, its parameters numbered for the database:

```go
func (r Order) MonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error) {
	const q = `SELECT date_trunc('month', placed_at) AS month, count(*) AS count
FROM orders
WHERE placed_at >= $1
GROUP BY 1
ORDER BY 1`
	// ...
}
```

- A parameter is `:` followed by a name, as `:since` is. A `:` after another `:`, a letter, a digit, `_`, or `]`
  starts none, so a cast such as `::int` and a slice such as `arr[1:n]` are left alone, as is anything in a string, a
  quoted name, a comment, or a dollar-quoted body. `describe` prepares what results, so a misreading shows there.
- A query's parameters take their types from the method. A struct argument provides one through each of its exported
  fields, by the field's name in lower camel case, as `:customerID` for `CustomerID`, whatever its column. A name used
  by SQL must match exactly one argument or field: a parameter of a field's name stops generation. This lets a model be
  written without a separate parameter DTO. The `nullzero` option also applies to these field arguments.
- Result columns go directly into model fields by column name and tag. A one-column result can instead be a scalar
  or enum, and `[]T` can collect those values directly. `T` returns one row or `orm.ErrNotFound`; `[]T` returns every
  row. With no result columns, `(int64, error)` reports affected rows; with one bigint column, it scans that value.
  An `error`-only method returns no columns. Inputs and results are checked against the snapshot.
- Each result column needs a name of its own, since the fields take them by name; MySQL cannot even describe a query
  whose columns share a name. Give them aliases, as in `SELECT o.id, c.id AS customer_id`.
- A result column that may be NULL needs a field that can be nil, or a string field tagged `nullzero`.
  Other fields that cannot be nil stop generation where
  the database says its column may be NULL; where the database cannot tell, the field's type is taken at its word.
  MySQL tells for every column. PostgreSQL tells only for a column read straight from a table, and as the table
  declares it, even through an outer join that leaves it NULL: hold such a column, as `c.email` from
  `LEFT JOIN customers c`, in a pointer. kanna-orm's own queries load relations with queries of their own, not joins,
  so this is a matter for hand-written queries only.
- A scope has no method declared for it, so its signature is written on its line, and the lines of comment right
  above that line are its method's doc. MySQL does not tell the types of a statement's parameters, and this way it
  need not. A type of another package is named as the Go files of the scope type's package import it, or, by a name
  they do not import, as the file declaring the model of the scope type's table imports it, or by the name of the
  model's own package, as in `model.Status`, so that a package of scope types `//kanna:scopes` declares needs no import
  of its own for its models and the types they use. The package's own imports come first, as in Go, so one there wins
  over the model's file and package for its name. A package none of these names is imported in a Go file of the scope
  type's package for this alone, as in `import _ "time"`.
- A scope can also be written as a statement on its table, as
  `SELECT 1 FROM orders WHERE date_trunc('month', placed_at) = date_trunc('month', :at::timestamptz);` is the one above,
  so that a SQL linter, which cannot parse a condition alone, reads the file as it reads the queries. kanna-orm takes
  the condition alone into statements of its own, so the statement has that shape and no other: it selects `1` from the
  scope type's table, gives the table no alias, which would mean nothing where the condition goes, and holds nothing
  after the condition but a `;`. The table's name is read as the database reads it: quoted, as it is written, and
  unquoted, in lower case on PostgreSQL, which folds it so, and in any case on MySQL, whose rule depends on how its
  server is set up. Another statement starting with `SELECT` stops generation; a condition such as `id IN (SELECT ...)`
  is a condition as before.
- A statement runs from its line to the next statement's comment or line. The blank lines and the lines of comment it
  ends in are not part of it, nor is a comment at its very end, so that a scope can end in one.
- On MySQL, a parameter used twice is passed twice, once for each `?`.
- A slice argument, other than `[]byte` or a type with a `Value` method, is a list of values, and a nil one a list of
  none, as an empty one is. PostgreSQL takes it as an array, as in `= ANY(:ids)`, and a nil one as an empty array, not
  `NULL`. On MySQL, it is written with a `?` for each of its values when the method runs. With none, right between the
  parentheses of `IN`, it is the empty set, a subquery of no rows: `IN (:ids)` matches no row, and `NOT IN (:ids)`
  every row, as PostgreSQL's `= ANY` and `<> ALL` do. Anywhere else, as in `FIELD(id, :ids)`, it is `NULL`.
- pgx cannot write an array of a type it does not know, such as an enum type, and kanna-orm does not rewrite your SQL,
  so cast the parameter from `text[]` yourself: `state = ANY(:states::text[]::ticket_state[])`.

### Scopes on a query

A query whose method takes scopes, as `ListWithCustomers(ctx, s ...scopes.Order)`, is read as a table of its own: its
result, read as the rows of the scopes' table, is what the scopes narrow, order, and page, and load the relations of.
kanna-orm writes the query in a `WITH` named after the method, and the scopes after it:

```sql
-- query: ListWithCustomers
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, c.name AS customer_name
FROM orders o
JOIN customers c ON c.id = o.customer_id;
```

```go
// OrderWithCustomer is an order with the name of its customer, which the query adds to it.
type OrderWithCustomer struct {
	model.Order
	CustomerName string
}

ListWithCustomers(ctx context.Context, s ...scopes.Order) ([]OrderWithCustomer, error)
```

```sql
WITH list_with_customers AS (SELECT o.id, ..., c.name AS customer_name FROM orders o JOIN customers c ON ...)
SELECT orders.id, ..., orders.customer_name FROM list_with_customers AS orders WHERE (status = $1) ORDER BY id
```

- It is a `Get`, a `Find`, a `First`, or a `List`, and reads rows of the scopes' model, or of a struct embedding it by
  value, whose other fields hold the columns the query adds. The columns of the result and the fields, those of the
  model among them, match one for one, and a column the query adds may not be one of the model's. A query that takes
  no scope returns what it likes, as ever.
- The query's parameters come first, then those of the scopes.
- A `LIMIT` in the query keeps its rows before the scopes narrow them. An `ORDER BY` in it orders nothing the scopes
  read, so one without a `LIMIT` gets a warning: order the rows by the scopes.
- A lock in the query, as `FOR UPDATE SKIP LOCKED`, locks the rows the query reads before the scopes narrow and page
  them: with `Limit(1)`, it locks every row of the query, and a second worker skipping them gets none. Such a lock gets
  a warning: to lock the rows of a page, write its condition and its `LIMIT` in the query, and take no scopes.
  `//kanna:lock` on such a method stops generation, as on any query written by hand.
- `After`, `Before`, and `Cursor` continue a page by the primary key of the model, so the rows of the result must be
  told apart by it. A row that appears twice gets its relations twice, one set each.
- How fast it runs is for the database to say, and kanna-orm promises nothing of it: PostgreSQL 12 and later read a
  `WITH` used once into the statement, and MySQL may read it so or read it whole first. When it is slow, put the
  conditions that narrow it in the query.

### Adding a value related by other columns

A value related to a row by other columns than a foreign key, such as who approved the revision an order is at, is
read by a query of your own. Read the rows as ever, with their scopes, cursor, and relations, then the values for their
keys in one more query, and put the two together in Go:

```sql
-- query: ListApprovers
SELECT o.id AS order_id, a.approver
FROM orders AS o
LEFT JOIN approvals AS a ON a.order_id = o.id AND a.revision = o.revision AND a.revoked_at IS NULL
WHERE o.id = ANY(:ids) -- IN (:ids) on MySQL
ORDER BY o.id;
```

```go
orders, err := repo.List(ctx, scopes.Orders().Open().PlacedAtDesc().Limit(20).WithLines())
// ...
ids := make([]model.OrderID, len(orders))
for i, o := range orders {
	ids[i] = o.ID
}
approvers, err := repo.ListApprovers(ctx, ids)
```

The [examples](example/postgres/main.go) read the approvers of their orders this way.

## Checking the scopes you pass

Some scopes make a repository method fail every time, before it sends any SQL. `Count` takes conditions alone, so a
scope that orders the rows makes it return `orm: Count takes conditions alone, but its scope orders the rows`.
`kanna-orm vet` finds such calls without running them:

```sh
go tool kanna-orm vet ./...
```

```text
usecase/orders.go:42:21: error: Count takes conditions alone, but its scope orders the rows
  scopes/scopes.go:22:11: Latest orders them by PlacedAtDesc
```

It reports a call at the method it calls, in the words of the error the call would return, and, when what makes it
fail is written elsewhere, such as in a scope of your own, where that is. It finds:

- an order, paging, or a relation to load in a scope given to what takes conditions alone: `Count`, `Exists`, `Sum`,
  `Min`, `Max`, `Avg`, a `List` of a projection of aggregates, an `Update` taking a unique key or a whole row, and the
  alternatives of `Or` and `Not`; paging given to `Get` and `Find`; a relation to load given to a projection, a read of
  one column, a `Delete`, or an `Update` with a change set;
-  a `Delete`, or an `Update` with a change set, whose scope has no condition, which would write every row; a `Delete`,
  or an `Update` of a change set taking no unique key, ordered without `Limit` or `Offset`, or paged on a table with
  neither a primary key nor a non-null unique key;
- `After` or `Before` in the scope a relation is loaded by, and `Limit` or `Offset` in that of a relation to one row;
- a negative constant given to `Limit` or `Offset`; a change set that assigns nothing, given to an `Update`; one that
  assigns a column its method cannot write, such as the one an upsert meets its row by.

vet follows a scope through the variables of a function, the branches that build it, the closures that read it, and
the scope methods and functions you write. A call through an interface is checked as the implementations kanna-orm
generated for it would run it. vet reports a call only when every path to it fails. It leaves alone a call that fails
on some paths only, as one given a scope paged past the first page only does, and a scope it does not follow: one that a
parameter, a field, a package variable, or a slice holds. Test files are not checked, as a test may make a call fail on
purpose. A package that passes holds no call vet found to fail; that is not to say there is none.

## Drivers and dialects

The snapshot knows which database it came from, so only the driver is chosen, with `-driver`:

| Database           | `-driver`                                                | The generated code uses                             |
|--------------------|----------------------------------------------------------|-----------------------------------------------------|
| PostgreSQL         | `pgx`, the default                                       | `pgx.Rows`, `$1`, `= ANY($1)`, `RETURNING`          |
| PostgreSQL         | `database/sql`, through pgx/stdlib                       | `*sql.Rows`, `$1`, `= ANY($1)`, `RETURNING`         |
| MySQL 8.4 or later | `database/sql`, the default, through go-sql-driver/mysql | `*sql.Rows`, `?`, `IN (?, ...)`, `LAST_INSERT_ID()` |

go-sql-driver/mysql reads a `DATETIME` or a `TIMESTAMP` into a `time.Time` only when its DSN has `parseTime=true`, so
the DSN the application opens needs it: without it, a read of a time returns an error saying so, and an `Insert`
returning the model returns it [after writing its row](#writing). The driver then writes a time in the location `loc`
names, UTC unless it names another, and reads a `DATETIME`, which holds no zone, as a time there. Give every program
that reads and writes the database the same `loc`, and, for a `TIMESTAMP`, which MySQL converts by the session's
`time_zone`, the same zone there too.

The code differs for each, and none of it hides the database behind an abstraction: where PostgreSQL and MySQL differ,
the difference is settled when the code is generated.

|                      | PostgreSQL                                          | MySQL                                        |
|----------------------|-----------------------------------------------------|----------------------------------------------|
| A name, quoted       | `"x"`                                               | `` `x` ``                                    |
| Parameters           | `$1`, used again where needed                       | `?`, one for each use                        |
| A list of keys       | `= ANY($1)`                                         | `IN (?, ...)`, split when long               |
| Relation keys        | `unnest(...)`, joined                               | `VALUES ROW(...)`, joined, split when long   |
| Reading a write back | `RETURNING`                                         | `LAST_INSERT_ID()` and a `SELECT` by the key |
| Upsert               | `ON CONFLICT (...)` on the key named                | `ON DUPLICATE KEY UPDATE` on any unique key  |
| Many rows            | `unnest`, in one statement                          | `VALUES`, split when long                    |
| Copying many rows    | `COPY`, through `CopyFrom` of pgx alone             | none                                         |
| Paging by key        | a row comparison, unless the order mixes directions | spelled out                                  |

A value type goes to and from the database through its `Value` and `Scan` methods; any other type, as the driver
converts it. A column that may be NULL is held by a pointer. Through database/sql, an array of PostgreSQL is read into
a slice on Go 1.27 or later, where pgx/stdlib scans each column itself.

## With the rest of kanna

- [kanna-value](https://github.com/go-kanna/value): a value type is read through `Scan`, which checks its rule, so a
  value the database holds but the rule rejects is an error naming its column. A cursor is decoded the same way.
- [kanna-di](https://github.com/go-kanna/di): `NewOrder` is an ordinary constructor, which a container can
  provide like any other.
- [kanna-fixture](https://github.com/go-kanna/fixture): a fixture is a model like any other, so `Insert` puts it into a
  test's database.
- [kanna-mapper](https://github.com/go-kanna/mapper): rows are read into your models directly, so there is no row type
  to map; map the models to the wire as before.

## What kanna-orm does not do

- Track changes, keep an identity map, or load lazily: a method runs the statements its declaration says, when it is
  called.
- Take a zero value for the column's default: a zero is written as a value, and `orm:",default"` says the database
  assigns one.
- Generate arbitrary joins or nested query shapes, such as subqueries, CTEs, or UNION, from scopes. Joins through
  foreign keys are supported for read projections, INNER or LEFT, and so is grouping by the fields of a projection of
  aggregates; other query shapes can use [hand-written SQL](#hand-written-sql).
- Run one generated package on both databases: the types are those one database answers.
- Create or apply migrations. Use goose or any other tool, and describe the result.

## Not yet

These are out of what kanna-orm does so far, not ruled out:

- HAVING, and an order by an aggregate, of a [projection of aggregates](#reading-aggregates). Write them in
  [SQL](#hand-written-sql) returning a DTO.
- Write relations. Saving a row along with what it refers to is yours to sequence, in a transaction.
- Page by a column that may be NULL.
- Support MariaDB or SQLite.

## Flags

```sh
kanna-orm [flags] [packages]
kanna-orm describe [flags] [packages]
kanna-orm vet [flags] [packages]
```

Generating:

| Flag                 | Meaning                                                                                                     |
|----------------------|-------------------------------------------------------------------------------------------------------------|
| `-destination <dir>` | write repository implementations into dir; required for repository interfaces; `.` keeps them in the interface package |
| `-driver <name>`     | `pgx` or `database/sql` (default: `pgx` on PostgreSQL, `database/sql` on MySQL)                             |
| `-snapshot <file>`   | the snapshot to read (default: `kanna-orm.snapshot.json` at the root of the module)                         |
| `-tags <list>`       | comma-separated build tags                                                                                  |
| `-check`             | verify the output is up to date instead of writing it, including that no output is left over                |
| `-v`                 | list the files written, removed, or left unchanged                                                          |
| `-version`           | print the version                                                                                           |

Describing:

| Flag                  | Meaning                                                                              |
|-----------------------|--------------------------------------------------------------------------------------|
| `-database-url <url>` | the database to describe (default: `$DATABASE_URL`)                                  |
| `-snapshot <file>`    | the snapshot to write (default: `kanna-orm.snapshot.json` at the root of the module) |
| `-tags <list>`        | comma-separated build tags                                                           |
| `-check`              | verify the snapshot is up to date with the database instead of writing it            |
| `-v`                  | say whether the snapshot was written or left unchanged                               |

Vetting:

| Flag           | Meaning                    |
|----------------|----------------------------|
| `-tags <list>` | comma-separated build tags |

The packages default to the one in the current directory, so `//go:generate go tool kanna-orm` needs no argument. As
with every kanna generator, `KANNA_CHECK=1 go generate ./...` checks instead of writing. A run that succeeds prints
nothing but its warnings; one that finds neither a `//kanna:scope` type nor a `//kanna:repository` interface says so. A
package whose scope declarations are all gone has its `scopes_gen.go` removed. A destination whose repository interfaces
are all gone has its `repository_gen.go` removed, unless code in the packages the run loads still calls what it
declares; a file of that name without the generated-code header is never touched. The `go generate` that removes one
ends with `open ...: no such file or directory`, from go generate itself, which lists a package's files before it runs
the generators; the file is gone as it should be ([golang/go#36068](https://github.com/golang/go/issues/36068)).

## Example

[`example`](example) is an order service with customers, products, orders, and their lines, shipments, and tags,
generated once for PostgreSQL through pgx, in [`example/postgres`](example/postgres), and once for MySQL through
database/sql, in [`example/mysql`](example/mysql): each a module of its own, as a table has one scope type in a module.
It has scopes of its own, in Go and in SQL, keyset paging with cursors, each kind of relation, an upsert by a natural
key, many rows written at once, queries written by hand, and rows locked in transactions. `make example` migrates the
databases of `make db-up`, describes them, regenerates the example, checks it with `kanna-orm vet`, and runs it against
each; CI does the same, and fails if the output would change or vet reports a call.

## Development

```sh
make test       # unit tests
make db-up      # start the PostgreSQL and the MySQL of compose.yaml, on ports 15432 and 13306
make test-db    # unit tests, and the tests against those databases
make lint       # golangci-lint
make example    # migrate and describe the databases of db-up, regenerate the example, vet it, and run it
make bench      # run the benchmarks in a container next to the PostgreSQL of db-up
```

[`bench/postgres`](bench/postgres) measures kanna-orm against pgx alone, sqlc, GORM, bun, and ent; its README has the
results, and how they were taken.

## License

[MIT](./LICENSE)
