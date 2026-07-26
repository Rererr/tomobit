# Schema Design v1.0

- Status: **Fixed — レビュー済み（D1〜D11全承認、R1〜R4確定）**
- Date: 2026-07-15
- 関連: [ADR-0004](../decisions/ADR-0004-tech-stack.md)（真実と射影の分離）, [ADR-0002](../decisions/ADR-0002-surprise-and-split-judgment.md), [ADR-0003](../decisions/ADR-0003-outcome-and-preference.md), [ADR-0005](../decisions/ADR-0005-perception-model-and-schema-boundary.md)（形と意味の境界）

---

## 全体像

```text
真実（追記専用・トリガーで不変性を強制）
  events            生ログ。バージョン付きJSON封筒
  experiences       不変の資産。再知覚はバージョン共存で

射影（全行DELETE → tomobit rebuild で再生できる）
  connections       能力・好み両方。(α, β, last_update)
  surprise_ledger   ADR-0002の台帳

状態（真実でも射影でもない、忘れてはいけない作業記憶）
  curiosity_queue   「気になったことを忘れない」
```

sessionsテーブルは**作らない**（eventsから導出するビュー）。
Deferred Perceptionの未処理キューも**作らない**
（「最新extractorのExperienceを持たないSession」というクエリで導出できる）。

---

## DDL

```sql
PRAGMA journal_mode = WAL;

-- ============ 真実 ============

CREATE TABLE events (
  id         INTEGER PRIMARY KEY,   -- 追記順 = Replay順
  session_id TEXT    NOT NULL,      -- ULID
  seq        INTEGER NOT NULL,      -- Session内の順序
  ts         INTEGER NOT NULL,      -- unix ms
  v          INTEGER NOT NULL DEFAULT 1,
  type       TEXT    NOT NULL,      -- 'task.started' 'provider.finished' など
  payload    TEXT    NOT NULL CHECK (json_valid(payload)),
  UNIQUE (session_id, seq)
);
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
  BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
  BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;

CREATE TABLE experiences (
  id         TEXT    PRIMARY KEY,   -- ULID
  session_id TEXT    NOT NULL,
  ts         INTEGER NOT NULL,      -- 対象の出来事の時刻（抽出時刻ではない）
  kind       TEXT    NOT NULL CHECK (kind IN ('execution','preference')),
  extractor_ver   INTEGER NOT NULL, -- 抽出ロジック版数（プロンプト/schema改版で+1）
  extractor_model TEXT    NOT NULL, -- 例 'qwen3:8b' 出自の記録
  context    TEXT    NOT NULL CHECK (json_valid(context)),
                                    -- {"cap":"implement","lang":"rust",
                                    --  "topic":"lifetime","model":"opus-4.8"}
  provider   TEXT,                  -- execution: 実行者 / preference: NULL
  outcome    TEXT    NOT NULL CHECK (json_valid(outcome)),
  source     TEXT    NOT NULL DEFAULT 'production'
             CHECK (source IN ('production','learning'))
);
CREATE INDEX experiences_by_session ON experiences (session_id);
CREATE TRIGGER experiences_no_update BEFORE UPDATE ON experiences
  BEGIN SELECT RAISE(ABORT, 'experiences is append-only'); END;
CREATE TRIGGER experiences_no_delete BEFORE DELETE ON experiences
  BEGIN SELECT RAISE(ABORT, 'experiences is append-only'); END;

-- 再知覚の共存: 同一sessionに複数版数の行を許し、
-- 「現在の知覚」はビューで選ぶ（整数比較）
CREATE VIEW experiences_current AS
  SELECT * FROM experiences e
  WHERE e.extractor_ver = (
    SELECT max(extractor_ver) FROM experiences
    WHERE session_id = e.session_id AND kind = e.kind
  );

-- ============ 射影 ============

CREATE TABLE connections (
  kind        TEXT    NOT NULL CHECK (kind IN ('capability','preference')),
  scope_key   TEXT    NOT NULL,     -- 正規化トークンの'|'連結
                                    -- 'cap=implement|lang=rust'
  target      TEXT    NOT NULL,     -- 'claude' / preference: 'claude~codex'(辞書順)
  alpha       REAL    NOT NULL,     -- 減衰済み擬似カウント（小数）
  beta        REAL    NOT NULL,
  last_update INTEGER NOT NULL,     -- lazy decayの錨
  born_ts     INTEGER NOT NULL,
  parent_key  TEXT,                 -- Split系譜（Merge先の解決に使う）
  PRIMARY KEY (kind, scope_key, target)
);

CREATE TABLE surprise_ledger (
  kind          TEXT    NOT NULL,
  scope_key     TEXT    NOT NULL,
  target        TEXT    NOT NULL,
  experience_id TEXT    NOT NULL,
  ts            INTEGER NOT NULL,
  p             REAL    NOT NULL,   -- 観測時点の予測
  y             REAL    NOT NULL,   -- 重み解決後のOutcome (0..1)
  s_excess      REAL    NOT NULL,
  PRIMARY KEY (kind, scope_key, target, experience_id)
);

-- ============ 状態 ============

CREATE TABLE curiosity_queue (
  id          TEXT    PRIMARY KEY,  -- ULID
  created_ts  INTEGER NOT NULL,
  signal      TEXT    NOT NULL,     -- 'questioned'|'preference_gap'|'knowledge_gap'|
                                    -- 'new_provider'|'model_update'|'environment_change'
  payload     TEXT    NOT NULL CHECK (json_valid(payload)),
  priority    REAL    NOT NULL,
  status      TEXT    NOT NULL DEFAULT 'pending'
              CHECK (status IN ('pending','done','dismissed','expired')),
  resolved_ts INTEGER
);
```

---

# Decisions — 根拠とPros/Cons

## D1. 時刻は INTEGER unix ms

**根拠**: Decayは読むたびにΔtを計算する（lazy decay）。時刻は表示用データではなく**計算の入力**。

- Pros: 減衰計算が直接できる / タイムゾーン事故がない / 比較・索引が速い
- Cons: 生SQLで読みにくい（`datetime(ts/1000,'unixepoch')`で緩和）

## D2. ID戦略 — eventsは連番、それ以外はULID

**根拠**: eventsのINTEGER PRIMARY KEYはSQLiteのrowidそのものになり、**追記順＝Replay順**が構造で保証される。Session/ExperienceはULID（時刻順ソート可能・分散生成可能・26文字）。

- Pros: Replay順に一切の曖昧さがない / ULIDはログで読める・時系列に並ぶ
- Cons: 2種類のID体系が混在（役割が違うので妥当と判断）

## D3. eventsはバージョン付きJSON封筒＋追記専用トリガー

**根拠**: Event型は実装初期に最も激しく増減する。型ごとにテーブル/カラムを切ると、真実テーブルにマイグレーションが波及する。封筒（v, type, payload）なら**列は永遠に増えない**。不変性はアプリの規律でなくDBトリガーで強制する。

- Pros: 真実テーブルのマイグレーション地獄が構造的に起きない / 将来の再解釈（v違いの読み替え）はGo側の純関数で済む / トリガーはバグや手癖のUPDATEを物理的に弾く
- Cons: payloadの型安全性はDBでは保証されない（Go側でtype→struct検証） / トリガーは意図的な保守作業でも邪魔（その時だけDROP）
- 対案: 型ごとの正規化テーブル → 初期の反復速度を殺すため却下

## D4. experiencesも追記専用。再知覚は「extractorバージョン共存＋current view」

**根拠**: Perception（Ollama抽出）は改善される。旧Experienceを書き換えると「Experience is Immutable」が崩れる。**同一Sessionに複数版数の行を許し、最新をビューで選ぶ**なら、追記だけで再知覚が表現できる。

版数は`extractor_ver INTEGER`＋`extractor_model TEXT`の2列（R1確定）。
「最新」は整数比較で決まり、文字列辞書順のハックは存在しない。
ADR-0005の通り、改版の本体はプロンプト/schemaであり、
**プロンプトかschemaを変えたらverを+1する**。モデル差し替えだけならmodel列の変化として残る。

- Pros: 不変性が完全に保たれる / 新旧の知覚を比較できる（抽出品質のdogfood、ADR-0004） / rebuildは常に`experiences_current`を読めばよい / 版数比較が頑健
- Cons: ストレージ重複（dogfood規模では無視できる） / 列が1本増える

> 追記（ADR-0034）: 忘却は世代を跨いで到達する。`forget --id`は指名された
> 行に加え、同じ(session_id, kind)でextractor_verがそれ未満の全行を同一
> トランザクションで削除する。`experiences_current`が(session, kind)ごとに
> max(extractor_ver)の世代を選ぶ以上、現行世代の行だけを消すとビューの
> 選択が一つ下の世代へ落ちてしまう — 版数共存の選択規則そのものに由来する
> 巻き戻り経路であり、個別ケースを塞ぐのではなく到達範囲を構造的に閉じる
> （詳細はADR-0034）。

## D5. Contextは JSON map、正規化トークンは "key=value"

**根拠**: Split審判（ADR-0002）は「属性の有無で層別したカウント」を必要とする。属性を`lang=rust`のような**アトミックなトークン**として扱えば、latticeの数学（部分集合）と実装（文字列集合）が一対一になる。

正規化規則（`core.CanonValue`）: Unicode制御文字除去（ESC/CSIなど — LLM抽出値に
紛れた端末制御シーケンスがscopeまで届くのを断つ）→ 前後空白除去 → 小文字化の順。
**順序に意図がある**: 制御文字を先に落とさないと、それが新しい端に空白を隠す。
併せて`|`は`-`へ写像する（2026-07-21追加。scope_keyのトークン区切り文字と衝突すると
`ParseScopeKey`で1トークンが2つに割れ、Connectionが自分のscopeに二度と
SubsetOf一致しなくなるため）。keyとvalueは`=`連結、scope_keyはトークンを
辞書順ソートして`|`連結。**NFC正規化は未実装**（合成済み/分解済みで異なる
Unicode表現を持つ値（例: "café"の`é`が単一コードポイントか"e"+結合アクセント
記号かの違い）は別トークンとして扱われる。必要かどうかは別議題として残す）。

語彙管理はADR-0005の境界をそのまま適用する（R2確定）:

```text
key    「形」→ JSON schemaのenumで閉じる
       初期セット: cap / lang / framework / topic / size / review / model
       （review は最小コア v1 では未実装。追加時は extractor_ver +1）
       key追加 = schema改版 = extractor_ver +1 として記録に残る

value  「意味」→ プロンプトが担う
       既存語彙を抽出プロンプトに渡し、再利用を促す
       （axum / Axum / axum-web の割れを防ぐ）
       LLM抽出対象の4key（lang/framework/topic/size — cap/model/providerは
       決定的パースなので対象外）ごとに上限20件（`perceive.capVocab`、
       vocabLimit）。選抜は(1)値が出現したdistinct session数（experience行
       数ではない — 1セッションの複数preferenceが同じContextを複製コピー
       するため、行数だと水増しになる）の降順 (2)直近出現tsの降順
       (3)値の辞書順、の三段ランキングで上位を残し、提示順はアルファベット順
       に戻す
```

- Pros: scope照合が文字列比較になる / 審判はGo側でexperiencesを読んでカウント（数千行なら毎回全読みでもミリ秒） / EAV表もJSON1インデックスも初版では不要 / keyの暴走はschemaが物理的に防ぐ
- Cons: valueの語彙品質はプロンプト誘導頼み（抽出品質のdogfoodで監視）
- 対案: EAV join表 → dogfood規模では過剰。必要になれば**射影として**後付けできる（真実は無傷）

## D6. capabilityもContext属性に畳む（`cap=implement`）

**根拠**: Connectionを「(Context属性集合) → Provider」に統一したい。capabilityを特別な列にすると、latticeが「capability × 属性集合」の2次元になり、Split審判が2系統要る。属性に畳めば**1つのlatticeと1つの審判**で済み、しかも「cap=reviewの時だけ」というSplitを審判が自然に発見できる。

- Pros: 機構が1系統 / capability横断の知識（`lang=rust`→Claude全般に強い）が粗い粒度として自然に存在できる
- Cons: 「Experienceは必ずcapabilityを持つ」がスキーマで強制されない（Go側の抽出バリデーションで担保） / EXPERIENCE.mdの図とは見た目が変わる（意味は同じ）

## D7. Preferenceも同じexperiencesテーブル（kind判別）

**根拠**: ADR-0003で「Tomoの質問への回答＝Learning Reality→Experience」と決めた。別テーブルにすると Replay・不変性トリガー・extractor管理がすべて二重になる。

- Pros: 単一のReplayストリーム / パイプラインが1本 / 「Experienceは資産」が単数形のまま
- Cons: 列の意味がkindで揺れる（preferenceではprovider=NULL、outcomeは`{"preferred":"codex","over":"claude"}`）→ 型の揺れはGo側のkind別structで吸収
- 対案: preferences専用テーブル → 器官が増えるだけと判断し却下

## D8. connectionsは (kind, scope_key, target) が主キー、α/βはREAL

**根拠**: One Ledger——行の実体はBeta(α,β)と減衰の錨(last_update)だけ。Strength/Confidence等の列は**作らない**（保存された属性は嘘をつく、CONNECTION_ENGINE.md）。減衰で擬似カウントは小数になるのでREAL。

preferenceのtargetは辞書順ペア`claude~codex`（α=辞書順で先の勝ち数、β=後の勝ち数）。順序を正規化しないと同じペアが2行に割れる。

Provider名は**道具名のみ**（R3確定）: `claude-code` / `codex` / `ollama`。
Adapter登録名に固定し、自由入力を許さない（表記揺れの根絶）。
モデルバージョンはContext属性トークン`model=opus-4.8`として出す。

> モデル更新で履歴が途切れない。モデル差が効く時だけ、
> Split審判がそれを発見する（ADR-0001の機構がモデル更新問題を解く）。
> 効かなくなればMergeが畳む。CuriosityのModel Updateシグナルは
> 「model属性の新値の登場」として検出できる。

- Pros: ライフサイクル状態・導出値の保存列ゼロ＝二重帳簿ゼロ / lazy decayに必要な情報が揃っている / モデル更新のコールドスタートが存在しない
- Cons: 一覧表示のたびに導出計算が要る（純関数なので問題なし）

## D9. surprise_ledgerは射影テーブル

**根拠**: ADR-0002の台帳。「Experienceログ＋その時点のConnection状態から再計算できる導出インデックス」なので射影側。pとyを保存するのは、審判とデバッグ（なぜQuestionedになったか）を追えるようにするため。

- Pros: rebuildで完全再生できる / Questionedの根拠が説明可能（Tomoが「最近Lifetimeで外し続けてる」と言える）
- Cons: 経験1件×マッチしたConnection数だけ行が増える（粗→Splitのおかげでマッチ数は小さく保たれる——ADR-0001の配当）

## D10. 単一DBファイル＋`tomobit rebuild`

**根拠**: 真実/射影を物理ファイルで分ける案（truth.db + derived.db）は思想的には美しいが、ATTACH管理と非原子的な跨ぎ書込みという実務コストを払う。単一ファイル＋「射影テーブルを全DELETEして再生する`rebuild`コマンド」で、再生成可能性は**コマンドとして**体現できる。

- Pros: 接続・トランザクション・バックアップが単純 / rebuildがいつでも試せる（＝再生成可能性が常時テストされる）
- Cons: 「真実だけバックアップ」が`.dump`のテーブル指定になる / 分離の物理的な美しさは失う
- 対案: 2ファイル分離 → Phase 2（デーモン化）で書込み主体が分かれた時に再検討

## D11. Outcomeの重み解決はGoの純関数、DBには生情報

**根拠**: ADR-0003の第1層重み（そのまま採用=1.0 / 軽微な手直し=0.7 / Revert=強い失敗）は**チューニング対象のノブ**。重みをexperiencesに焼き込むと、ノブを回すたびに真実が古くなる。experiencesには生の観測（`{"tests":"pass","adopted":"with-edits","reverted":false}`）だけを置き、重み→(Δα,Δβ)はGo側の純関数にする。

- Pros: 重み変更→rebuildだけで全歴史に新しい解釈が効く（Born with Historyと同じ思想）
- Cons: ledgerのyは「その時の重み」なので、重み変更後は台帳とconnectionsの整合はrebuildを要する（どちらも射影なので問題なし）

**Outcomeの生情報フィールド**（`core.Outcome`。重み解決は`OutcomeWeight`）: `tests_passed` / `adopted`（as-is|with-edits）/ `reverted`（ユーザーの主観的差し戻し）/ `verdict`（up|down・明示・全上書き）/ `cancelled`（中断＝無信号）/ `failed`（**客観**の実行失敗＝`provider.error`/exit≠0。ADR-0028 Decision 5で追加。`reverted`とは別フィールド — 主観の差し戻しと客観の失敗を混ぜないため）。`OutcomeWeight`第1層で`failed`→y=0。サブタスク／duel子は空の`task.finished`を持ち`failed`だけが信号になる。純関数を変えたので既存履歴はrebuildで再解釈、決定的パース（`provider.error`分岐）を変えたのでextractor_verをバンプ。

---

# 確定事項（レビュー済み 2026-07-15）

```text
R1. extractor版数 = extractor_ver INTEGER + extractor_model TEXT の2列
    プロンプト/schema改版でver+1。currentビューは整数比較

R2. Context語彙 = keyはschema enumで閉じ、valueはプロンプト誘導
    key初期セット: cap / lang / framework / topic / size / review / model
    （modelはR3の帰結として追加）

R3. Provider名 = 道具名のみ（Adapter登録名に固定）
    モデルはContext属性 model=... に出す

R4. eventsのtype初期カタログ（14種で確定）
    task.started / task.finished / task.cancelled / task.retried
    plan.generated / capability.started
    provider.selected / provider.output / provider.finished / provider.error
    test.result / user.verdict / user.preference / tomo.asked

    tomo.asked: Tomoの質問自体もReality。
    質問予算の管理と「聞きすぎていないか」の検証に使う。
    封筒方式のため、type追加はいつでも可能（このカタログは初版）

    test.result（初期カタログ。書き手は ADR-0052 で初めて生えた）:
    payload = {passed, exit_code, duration_ms, command}。読むのは passed だけで、
    残りは監査用（command が無いと passed:true が**何について真なのか**を
    後から言えない）。書くのは tomobit 自身がタスク境界で走らせた1本のコマンドの
    終了コードだけで、Providerの自己申告は受け取らない — test.result は
    OutcomeWeight で y を直接決めるため、宣言に Beta を動かす資格を与えない
    （ADR-0052 Decision 1）。**起動できなかった/タイムアウトした場合は記帳しない**:
    どちらも成果物についての判定ではなく、passed:false と書けば壊れたテスト環境が
    Providerの失敗として台帳に残る（同 Decision 4）。分割の子には走らせない
    （群間逐次では途中の赤が正常な中間状態であり、帰属できない — 同 Decision 3）

    **抽出プロンプトが見るのは「起きたこと」だけ**（ADR-0036 Decision 2d）:
    tomo.decided / plan.selected はハーネス自身の判断の記録であり、
    Reality ではない（PERCEPTION_ENGINE: Reality → Observation）。
    知覚の抽出プロンプトからは除外する — 載せると、事後の知覚が自分の
    事前推測（タスク知覚が置いた tokens）を読んで追認する経路になる。
    台帳からは消さない。監査は残り、見せる相手が変わるだけである

    追加済みtype（初版以後）:
    - tomo.decided（ADR-0012、ADR-0036で拡張）: 決定エンジンの監査記録。
      payload = {provider, seed(文字列 — UnixNanoはJSON float64の整数精度を
      超えるため), n, q, fallback, cap, size, tokens[...], candidates[{provider,
      quantile, passed, scope}]} と、劣化時のみ perception_degraded(文字列)。
      同じ台帳＋同じseed → 同じ判断のリプレイ用。tokens は**実際に判断が読んだ
      scopeトークン列**で、ADR-0036 でタスク知覚（LLM）が判断の入力に入ったため、
      「同じ台帳＋同じseed」だけでは判断を再現できなくなった — 再現の単位は
      台帳＋seed＋tokens になる。perception_degraded は知覚が失敗/未配線で
      決定的トークンだけで判断した理由（黙って劣化しない）
    - tomo.reflected（ADR-0015）: 語った事実の記帳。予算管理と重複抑止用。
      payload = {type, scope, provider, other, text, seed, after}
    - tomo.greeted（ADR-0019 D2）: おかえりを言った事実。同じ帰還への
      重複挨拶の抑止用。payload = {absent_ms}
    - task.turn（ADR-0022）: 対話セッションの2ターン目以降の依頼。
      payload = {intent, n}。1セッション=1タスクのまま、その中の往復を残す
      （task.startedのintentはタスクの最初の依頼＝そのタスクの意図）。
      決定的属性を持たないためparseDeterministicは読まない。抽出プロンプト/
      schemaは不変なのでextractor_verのバンプは不要（ADR-0006と同じ理屈）
    - plan.selected（ADR-0014、ADR-0036で拡張）: 採用したPlanの記帳（知覚の
      決定的抽出元 — experiences.plan列へ）。payload = {plan, cap, size, seed,
      n, q, fallback, tokens[...]} と劣化時のみ perception_degraded、
      または手動指定時 {plan, cap, manual: true}。tokens / perception_degraded
      の意味は tomo.decided と同じ
    - task.split（ADR-0023、ADR-0028で拡張）: 受理した分割提案の記帳。
      payload = {subtasks: [...], groups: [[...]], parallel_offered,
      parallel_accepted, est_cost_usd}。subtasks はフラットな実行順、
      groups は subtasks のインデックス群（例 [[0],[1,2],[3]] — Providerが
      独立と宣言した群。ADR-0028）、parallel_* / est_cost_usd は並走許可
      ゲートの提示・回答・概算（フラット提案では groups 以降を省略）。
      サブタスクは独立セッションで、その task.started は payload に
      parent: <親session_id> を持つ（source は production のまま）。
      知覚は task.split を読まず、抽出プロンプト/schemaも不変なので
      payload 拡張でも extractor_ver のバンプ不要（ADR-0006と同じ理屈）。
      **parent を持つセッションは既定で経験にならない**（ADR-0054 D2）:
      分割はタスク1つの内訳なので、そのタスクの経験は親の1件である。
      PendingSessions が子を列に入れない — イベントは1バイトも消さず、
      変わるのは投影だけ（One Ledger）。例外は duel の2側だけで、親が
      task.duel を持つことで自分を名指しで外す＝**将来の親子関係は
      既定で「内訳」に倒れ**、独立した発注として扱うには明示が要る。
      **子は tomo.decided を持たない**（ADR-0054 D1）: 決定はタスクにつき
      1回で、子は親が選ばれた相手をそのまま使う
    - user.forgot / user.amended（ADR-0033）: 忘却の器官の記帳。
      user.forgot payload = {ids: [...]}（経験単位forgetで消したid — 内容は
      載せない）、user.amended payload = {id, ver}（訂正元idと新世代）。
      このマーカーを持つセッションは PendingSessions（Deferred Perceptionの
      導出クエリ）から恒久的に除外される — 人間の知覚は最終知覚。
      experiences の削除は append-onlyトリガーの一時DROP（D3が予定した
      「意図的な保守」）を単一トランザクションで行い、COMMIT後に
      wal_checkpoint(TRUNCATE)+VACUUM で物理消去する。訂正は D4 の版数共存
      への追記（extractor_model='human'）で、トリガーには触れない
    （plan.generated は初期カタログ14種に含まれる。ADR-0014の提案記帳:
      payload = {cap, plan, parent, op} — メニューの生存はこのイベントから
      導出されるため rebuild で消えない）
    - task.workspace（ADR-0050）: Providerが宣言した作業場の隔離。
      payload = {isolated, kind, path} または {isolated: false, reason}。
      kind は**自由文字列**（"git worktree" / "jj workspace" / …）— 閉語彙に
      した瞬間に tomobit がVCSを知り始めるため、カタログ側でも列挙しない。
      **台帳が持つのは「Providerがそう宣言した」という事実であって、
      「隔離された」という事実ではない**（tomobitはVCSを知らないので検証しない —
      ADR-0050 Decision 2）。知覚は読まないので extractor_ver のバンプ不要
    - user.split_verdict（ADR-0051）: 分け方（采配）への評価。
      payload = {sid, provider, verdict, source}。verdict は "good" | "bad" |
      ""（無信号）、source は "feedback"（区切りのFeedbackに相乗り）|
      "question"（分割時の追加の問い）。**能力とは別の事実**なので
      task.finished の adopted/reverted には混ぜない（ADR-0003 Decision 2 の
      「負けた」と「できなかった」は別、と同じ理屈）。source を持つのは
      「分け方のせいではない」（無罪）と「分け方がよかった」（積極的評価）を
      後から区別できるようにするため。現時点では connections.kind に対応する
      賭け先を作らない（標本が貯まるまで信号だけ貯める — ADR-0051 Decision 1）。
      知覚は読まないので extractor_ver のバンプ不要

    列追加（ADR-0014）: experiences.plan（機械属性 — ハーネス自身が知って
    いる採用Plan。plan Connectionの賭け先キー）

    kind追加（ADR-0015）: experiences.kind に 'reflection'
    （反応の記帳。outcomeに insight / reaction を持つ）
```
