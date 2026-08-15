# Contract test matrix

| Change | Minimum evidence |
| --- | --- |
| Agent configuration | Required collaborator rejection and option effect |
| Model request | Ordered normalized messages and propagated context |
| Typed output | Decoder receives the response; invalid output retains cause and decode stage |
| Model failure | Source error is recoverable with `errors.Is`; model stage is inspectable |
| Tool execution | Declared metadata, typed run dependencies, tool result message, and tool error stage |
| Retry policy | Attempt count, terminal cause, and no retry for excluded errors |
| Streaming/concurrency | Context cancellation, completion signal, ordered events, and no goroutine leak path |

Favor a focused fake per test. A fake should record only the input the test needs to assert and return only the outcome it needs to drive.
