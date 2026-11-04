# PostgreSQL でのベンチマーク

[![English](https://img.shields.io/badge/lang-English-blue.svg)](README.md)

kanna-orm を、pgx 単体や、Go のプログラムでよく使われる ORM・コードジェネレータと並べて計測します。どれも同じデータベースの同じ行を、それぞれのドキュメントどおりの書き方で読み書きします。

```sh
make bench                                    # fill a schema of the database, check the subjects agree, and run them
make bench BENCH_FLAGS='-benchmem -count=10'  # ten runs of each, for benchstat
make bench-local                              # the same, run here against $(BENCH_POSTGRES) rather than in a container
make bench-gen                                # regenerate the code of sqlc and ent
```

`make bench` は、`compose.yaml` の `bench` コンテナで、`go.mod` の toolchain 行の Go を使ってベンチマークを実行します。データベースは `make db-up` の PostgreSQL で、動いていなければ起動します。往復は 2 つのコンテナの間のネットワークの中で済みます。ホストを経由すると、往復のほうが、ほとんどの対象自身の処理より高くつくからです。

## 対象

| 対象 | 内容 | 接続 |
|---|---|---|
| `pgx` | pgx v5.11.0。SQL と読み込みを手で書いたもの | pgxpool |
| `sqlc` | sqlc v1.31.1 | pgxpool |
| `kanna(pgx)` | このリポジトリの kanna-orm を `-driver pgx` で生成したもの | pgxpool |
| `kanna(sql.DB)` | 同じものを `-driver database/sql` で生成したもの | pgx/stdlib |
| `gorm` | GORM v1.31.2 と gorm.io/driver/postgres v1.6.3 | pgx/stdlib |
| `bun` | bun v1.3.0 | pgx/stdlib（simple protocol） |
| `ent` | ent v0.14.6（`sql/upsert` の機能つき） | pgx/stdlib |

対象はそれぞれ別のパッケージで、モデルも別々に持ち、それぞれのドキュメントどおりに書いています。選べるところは、次のようにしています。

- GORM は `SkipDefaultTransaction` を付けて書き込みます。付けないと、書き込みのたびにトランザクションを開始してコミットするので、ほかの対象にはない往復が 2 回増えます。
- bun は、[bun のドキュメント](https://bun.uptrace.dev/postgres/)どおり、pgx を simple protocol で使います。bun は引数をクエリ自体に埋め込むので、prepare するものがありません。
- sqlc は、ほかの対象と同じく、`timestamptz` を `time.Time` に、NULL になりうるカラムをポインタに読み込みます（pgtype の型にはしません）。sqlc のカタログは複数の配列の `unnest` を知らないので、複数行の Insert は配列ごとの `unnest` で書いています。
- それ以外は、それぞれの既定のままです。特に pgx は、bun 以外のすべての対象で、文を接続ごとに 1 回 prepare します。

## 操作

`migrate` は、example のテーブルとは別の `bench` スキーマに、顧客 1,000 人と、注文 10,000 件（そのうち 3 分の 1 が open）と、注文ごとに 5 行の明細を入れます。

| ベンチマーク | 読み書きするもの |
|---|---|
| `Get` | 注文 1 件を主キーで（毎回別の注文） |
| `List` | ある日以降に入った open の注文の最新 20 件: `status = $1 AND placed_at >= $2 ORDER BY placed_at DESC, id DESC LIMIT 20` |
| `ListWithLines` | 同じ注文と、その明細 100 行を 2 本目のクエリで。kanna-orm と ent は `WithLines`、GORM は `Preload`、bun は `Relation`、pgx と sqlc は手書き |
| `KeysetPage` | `List` の最後の注文より後の open の注文 20 件: `(placed_at, id) < ($2, $3)`。kanna-orm は `After` からこれを書きます |
| `Insert` | 顧客 1 人。ID を読み戻す |
| `InsertMany` | 1 つの注文の明細 100 行を 1 文で。ID を読み戻す。pgx、sqlc、kanna-orm は `unnest`、GORM、bun、ent は `VALUES (...), (...)` |
| `Upsert` | メールアドレスで突き合わせる顧客 1 人: `ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name`。ID を読み戻す |

- ベンチマークは `TestSameReads` と `TestSameWrites` のあとで実行します。この 2 つで、どの対象も同じ行を同じ順で読み、ほかの対象と同じものを書くことを確かめます。
- 書き込みのベンチマークは、次の対象に移る前に、書いたものを消してテーブルを vacuum します。
- kanna-orm は、書いた行のすべてのカラムを読み戻します。ほかの対象は ID だけを読み戻します。

## 結果

[#132](https://github.com/go-kanna/orm/pull/132) の時点の kanna-orm の数字です。2026-11-04 に `make bench BENCH_FLAGS='-benchmem -count=10'` で計測しました。環境は、`bench` コンテナの Go 1.26.9 と、別のコンテナの PostgreSQL 18.6 で、Apple M5 Pro（macOS 26.7.1）の OrbStack に 18 CPU をすべて割り当て、1 つの goroutine から実行しています。この数字は更新し続けるものではありません。今のコードの手元での数字は `make bench` で得られます。どれも 10 回の中央値で、benchstat で求めました。

確保の回数は対象自身の処理によるもので、回によって変わりません:

| allocs/op     | `pgx` | `sqlc` | `kanna(pgx)` | `kanna(sql.DB)` | `gorm` | `bun` | `ent` |
|---------------|------:|-------:|-------------:|----------------:|-------:|------:|------:|
| Get           |    16 |     16 |           16 |              42 |     85 |    52 |   120 |
| List          |   100 |    101 |           75 |             185 |    343 |   214 |   441 |
| ListWithLines |   518 |    528 |          246 |             678 |   1905 |  1267 |  1696 |
| KeysetPage    |   101 |    102 |           85 |             195 |    346 |   214 |   455 |
| Insert        |    17 |     17 |           21 |              45 |     71 |    35 |    78 |
| InsertMany    |   335 |    335 |          241 |             462 |    981 |   235 |  3688 |
| Upsert        |    15 |     15 |           19 |              42 |     77 |    33 |    96 |

時間はデータベースの処理を含み、回によって後ろのパーセントだけ揺れます（95% 信頼区間）:

| µs/op         |      `pgx` |     `sqlc` | `kanna(pgx)` | `kanna(sql.DB)` |      `gorm` |      `bun` |      `ent` |
|---------------|-----------:|-----------:|-------------:|----------------:|------------:|-----------:|-----------:|
| Get           |  14.3 ± 3% |  14.4 ± 3% |    14.4 ± 2% |      16.6 ± 11% |  25.3 ± 24% |  31.0 ± 3% |  24.0 ± 5% |
| List          |  26.1 ± 3% |  24.2 ± 3% |    25.4 ± 3% |       38.4 ± 4% |   62.2 ± 2% |  63.6 ± 4% |  47.5 ± 2% |
| ListWithLines |  86.1 ± 2% | 98.0 ± 14% |    89.6 ± 2% |      143.8 ± 3% |  215.9 ± 2% | 205.6 ± 5% | 157.6 ± 4% |
| KeysetPage    |  27.2 ± 4% |  26.0 ± 5% |    26.8 ± 3% |       38.9 ± 4% |   60.6 ± 3% |  65.6 ± 3% |  48.2 ± 2% |
| Insert        |  15.4 ± 3% |  15.5 ± 8% |    16.0 ± 4% |       18.6 ± 8% |   26.3 ± 7% | 38.2 ± 20% | 21.3 ± 10% |
| InsertMany    | 510.9 ± 3% | 524.5 ± 2% |   552.9 ± 2% |      587.8 ± 3% | 718.1 ± 12% | 606.9 ± 3% | 933.1 ± 4% |
| Upsert        |  18.5 ± 3% |  18.8 ± 3% |    19.3 ± 1% |       24.0 ± 3% |   30.9 ± 2% |  42.6 ± 8% |  27.6 ± 3% |

### 読み方

- 2 つのコンテナの間の往復は短く、pgx での `Get` 全体で 14 µs ほどです。そのため、対象自身の処理の差が見えます。ネットワーク越しのデータベースなら往復は何倍も遠く、同じ差はそれに比べて小さくなります。
- `pgx` が下限で、SQL をほぼ書いたとおりに実行する `sqlc` と `kanna(pgx)` は、その近くにいます。kanna-orm は、クエリごとに scope を組み立てて SQL にする分を含めても、どの読み込みでも sqlc との差が 1 µs ほどに収まり、確保の回数は sqlc 以下です。行は、ループの前に作った 1 つの行に読み込みます（sqlc は行ごとにループの中で宣言します）。注文の明細をキーで 1 本のクエリで読み、キーの番号で配る `ListWithLines` では、確保が sqlc の半分で、時間も sqlc 以下です。
- `Insert` と `Upsert` では、kanna-orm は行のすべてのカラムを読み戻し、pgx と sqlc は ID だけを読み戻します。その分が、kanna-orm の余分な確保と、0.5 µs ほどの差です。
- `kanna(pgx)` と `kanna(sql.DB)` は同じ SQL を実行するので、両者の差は database/sql の分です。`gorm`、`bun`、`ent` とは、同じく database/sql を通る `kanna(sql.DB)` と比べてください。
- `bun` は、引数を埋め込んだテキストとしてクエリを送るので、サーバーは毎回それを解析して計画を立てます。
- `InsertMany` では時間のほとんどがデータベースの処理です。kanna-orm は 100 行のすべてのカラムを読み戻し（pgx と sqlc は ID だけ）、sqlc より 5% ほど長くかかります。
