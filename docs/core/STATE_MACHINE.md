# STATE_MACHINE.md

# Tomobit State Machine

## 目的

Tomobit全体のライフサイクルを状態(State)とイベント(Event)で定義する。

State Machineは以下の責務を持つ。

-   現在状態の管理
-   状態遷移
-   イベント発火
-   Learning Engineへの通知
-   Tomoへの状態通知

------------------------------------------------------------------------

# 設計思想

State Machineは**Workflowの中身を管理しない**。

責務は、

-   どの状態にいるか
-   次にどの状態へ遷移するか

だけである。

Workflowの各処理は **WorkflowStep** として表現し、Execution Engine
が実行する。

    State Machine
            │
            ▼
    Executing
            │
            ▼
    Workflow
            │
            ▼
    WorkflowStep
            │
            ▼
    Executor
            │
            ├── LLM Executor
            ├── Shell Executor
            ├── Git Executor
            ├── Docker Executor
            ├── MCP Executor
            ├── Human Approval Executor
            └── Learning Executor
            │
            ▼
    Adapter

## WorkflowStepとは

WorkflowStepは「LLMを呼び出す処理」ではなく、

> **Tomobitが実行できる最小単位のアクション**

である。

例:

-   LLM実行
-   Shell実行
-   Git操作
-   Docker操作
-   MCP呼び出し
-   テスト実行
-   Human Approval
-   通知
-   Learning

LLMはWorkflowStepの実行方法の一つに過ぎない。

------------------------------------------------------------------------

# 基本状態

    Boot
     ↓
    Idle
     ↓
    Planning
     ↓
    Executing
     ↓
    Learning
     ↓
    Idle

※ Review・Test・Benchmark・Git Pushなどは State ではなく WorkflowStep
として扱う。

------------------------------------------------------------------------

# State一覧

  State       責務
  ----------- ----------------------------
  Boot        起動・初期化
  Idle        Task待機
  Planning    TaskからWorkflowを生成
  Executing   Workflowを順番に実行
  Learning    Experience DB更新・学習
  Error       復旧・人間介入待ち（将来）
  Shutdown    終了処理（将来）

------------------------------------------------------------------------

# 今後詳細化する項目

-   各StateのEntry/Exit Action
-   Event定義
-   State Context
-   Parallel Workflow
-   Retry
-   Cancel / Pause
-   Tomoとの完全な状態対応
