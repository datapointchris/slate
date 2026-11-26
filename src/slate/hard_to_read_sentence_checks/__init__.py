"""After the API writes a title or summary, the hard-to-read-sentences service scores it, and the write never waits.

`written_text` notes each new or changed title and summary as a write flushes, and hands them to
`sender` once the transaction commits. The sender's worker threads post each record's fields to
the service's `POST /find`, so no request thread or database session waits on the answer. A
script cannot rewrite its text, so the write never depends on the answer.

The service records every check it answers. The sender records each one that got no answer, and
`report_filing` files the `flagged-text` report that an answer carries, so a later job rewrites
the text. `docs/hard-to-read-sentence-checks.md` says why each part has its shape.
"""
