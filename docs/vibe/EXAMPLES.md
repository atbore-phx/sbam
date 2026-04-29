Examples and worked flows for Vibe Coding

Example: generate a plan from an issue and iterate

1. Open Copilot Chat in VS Code.
2. Run: `/generate-plan-from-issue 76`.
3. Confirm the suggested slug and answer clarifying questions.
4. Review the generated `TASK.md` and `PLAN.md` under
   `docs/implementations/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution/`.
5. Update the TASK/PLAN if needed, then implement on a feature branch.
6. Run the validation commands and open a draft PR for review.

Example: generate a local plan

1. Open Copilot Chat in VS Code.
2. Run: `/generate-plan-local my-feature`.
3. Answer clarifying questions to complete the TASK.
4. Inspect `docs/implementations/<slug>/` and proceed per the implementer
   checklist.

Tips
- Keep iterations small: shorter cycles make reviews easier.
- Add example outputs into the relevant TASK when updating prompts so future
  contributors know what to expect.
