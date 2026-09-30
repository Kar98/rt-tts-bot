## Writing style

Applies to everything you write: comments, docstrings, commit messages, PR descriptions, docs, and chat replies.

**Say the thing.** State what the code does, or why it does it, to a reader who has not seen your reasoning. If you use a private shorthand, define it or drop it.

**Cut.**
- If a word can go, cut it. Prefer the short word ("use", not "utilize" or "leverage").
- Use active voice. Turn nominalizations back into verbs ("validates", not "performs validation of").
- Drop signposting and hedges: "it is worth noting that", "as mentioned above", "this function is responsible for", "basically", "essentially", "simply", "in order to".
- Use plain words in place of jargon or stock figures of speech ("seamlessly", "robust", "under the hood", "at the end of the day").

**LLM tells to avoid.**
- Enthusiasm and narration: "Here we...", "Now we...", "Let's...", "Great!", "Certainly!".
- Em dashes used as a tic. Use a full stop or a comma.
- Closing summaries that repeat what the text already said.
- Bold, headers, or bullets on things that are one sentence.

**Comments.**
- Explain why, not what. Delete a comment that only restates the line under it.
- Don't rewrite a human-written comment into smoother, more generic text. Terse and slightly clumsy is fine. Change only the word or phrase that is wrong.
- Preserve directives (`noqa`, `eslint-disable`, `type: ignore`), doc tags, `TODO`/`FIXME` markers, and ticket references.

**Preserve specifics.** Keep names, numbers, ticket IDs, links and units. Don't add claims the code doesn't support. A rewrite longer than the original needs a reason.

Example:

    # Before
    # This function is responsible for leveraging the cache to ensure that
    # expensive lookups are seamlessly avoided where possible.

    # After
    # Return the cached user if we have one, to skip the DB lookup.

Already fine, leave it alone:

    # retry once: the auth service drops the first request after idle

Two things to check before pasting:
- Existing rules: it overlaps your "terse comments" memory and the ticket-prefix rule in this file's "Documenting knowledge" section. It doesn't contradict them.
- Emphasis: I left out the "Nothing fails when…" pattern as a named tell, since that's this line for it if you want it curbed.