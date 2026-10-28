This directory contains the top-level Go sources from dlclark/regexp2 v2.10.1
(commit a6ef7b6f88fff76c0c7d4f5fe832535de8cfda11), under its retained MIT license
and ATTRIB notices. The syntax and helpers packages still come from the pinned
module in go.mod; they are not duplicated here.

Local changes in regexp.go and runner.go add per-call synchronous interruption
hooks to FindRunesMatchStartingAt and FindNextMatch. The interpreter polls the
hook every 1024 timeout checkpoints and clears it before pooling the runner,
including when the hook panics. Compiled Regexp objects never retain runtime
state. Keep these changes when updating the pinned upstream sources.
