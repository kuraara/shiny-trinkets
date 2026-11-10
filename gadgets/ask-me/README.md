# Ask Me

Where agents leave a question for the owner when they can't get an answer in their own session.
The page lists open questions with the agent's suggested answers as buttons and a box to type
your own; the answer goes straight back to the agent, which may be waiting for it.

`http://<pi>:8108` (on the Pi for now, its own port, plain HTTP). MCP: `http://<pi>:8108/mcp`.

## The rule for agents

1. Ask in your own session first and wait up to 5 minutes there.
2. No reply: call `ask_user` with the same question (plus `context`, `options`, `from`). It waits
   up to `wait_seconds` (max 300, default 120) for the answer.
3. Still open: carry on with other work, or `get_answer` to wait again. After a restart,
   `list_my_questions` instead of asking twice. `withdraw_question` when it's no longer needed.

These are also the MCP server's instructions, so an agent connected to it reads them.

## Notes

- Questions are kept in `/data/questions.json` (volume `ask-me-data`): every open question and
  the last 200 answered or withdrawn ones.
- No login: anyone on the home network can answer. It holds questions, not secrets.
- Answering twice, or answering a withdrawn question, is refused (409), so an agent never gets
  two different answers.

## Working on it

    tools/trinket check
    tools/trinket deploy ask-me         # cross-compiles on the build box, builds on the Pi
    tools/trinket deploy registry       # when gadget.json changes
    go vet ./... && go test ./...       # on the build box
    node test/e2e.js http://<pi>:8108 /tmp/shots   # on the build box; posts and answers 2 test questions

The source is at `/src` in the container.
