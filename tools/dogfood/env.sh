# Shared environment for the synthetic dogfood. Source from run scripts.
DOG="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
DB="$DOG/dog.db"
TB="$DOG/tomobit"
export HOME="$DOG/home"              # isolate ~/.tomobit (config never read/written)
export TOMOBIT_FACE=0                # never spawn the face window
export TOMOBIT_CLAUDE_CONFIG_DIR=""  # "explicitly inherit" — satisfies the profile gate
PERC="--backend ollama --url http://127.0.0.1:11499 --model stub"
