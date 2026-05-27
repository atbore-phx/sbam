Contributing with Copilot or Claude Code (Vibe Coding)

Purpose
- Provide guidance for contributors using Copilot prompts or Claude Code
  slash commands to propose and implement changes.
- Both tools share the same prompt files in `.github/prompts/`.

Principles
- Iterate: generate a plan with `/generate-plan-*`, then refine it with
  clarifying answers.
- Validate: always run tests and perform manual review of generated code.
- Safety first: prefer draft PRs and require human approval before merging.

Recommended workflow
1. Generate a PLAN using `/generate-plan-from-issue <N>` or
   `/generate-plan-local <slug>`.
2. Review the generated `TASK.md` and `PLAN.md` under
   `docs/implementations/<feature>/`.
3. Answer clarifications added by the agent, update the TASK if needed.
4. Implement changes locally on a feature branch.
5. Add or update unit and integration tests as appropriate.
6. Run the implementer checklist (see `USAGE.md`).
7. Open a **draft PR** referencing the issue and the generated PLAN.
8. Request at least one human reviewer and ensure CI passes before converting
   from draft.

Guardrails (required before opening PR)
- Tests: run `make test` locally; the PR must pass CI tests.
- Review: at least one reviewer must sign off on generated code.
- Secrets: never commit real API keys or credentials; use placeholders and
  document how to configure them.
- Scope: keep each generated PR small and focused; prefer multiple smaller PRs
  over one large change.

Editing prompts
- Prompts live under [.github/prompts/](../../.github/prompts/).
- When adding or modifying prompts, document or update prompts in [PROMPTS.md](PROMPTS.md) add examples in [EXAMPLE.md](EXAMPLES.md) demonstrating expected
  outputs and guardrails.
- Document any changes to prompt behavior in the corresponding TASK/PLAN.

Contact
- For questions about the workflow, contact the repository owner or open an
  issue referencing this documentation.
