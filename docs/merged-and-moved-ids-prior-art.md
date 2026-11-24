# Prior art for a record that changes kind, merges into another, or answers to an old number

A promoted idea must become its initiative or issue under its own number, and an idea that
existing work covers must fold into that work. A fold leaves a number that has to keep leading
somewhere. This page
holds what other trackers and CRMs do when a record changes type, merges into another, or moves to
a new id, and what slate takes from each. [ideas.md](ideas.md) and [records.md](records.md) describe
the records themselves.

## A type change keeps the item, and a discovery tool's second record is what slate drops

- **Jira and Azure Boards change an item's type in place.** Jira's type change keeps the key.
  Azure Boards' "Change type" keeps the work item, records the change in its history, and resets
  its state to the new type's first state.
- **In class table inheritance, a type change moves the row between subtype tables.** slate's
  promotion must keep the `records` row, with its number, rank, pin and flow. It deletes the
  kind's own row and inserts the new kind's row, in one transaction. Every row keyed on the number
  stays where it is.

## A merge moves the loser's related rows to the survivor and keeps a pointer from the loser

- **Salesforce** merges up to three records. It deletes the losers, reparents every related record
  to the survivor, and sets `MasterRecordId` on each deleted loser.
- **Stack Overflow** merges a duplicate question into its target. The answers move to the target.
  The source stays as a stub that points at the target, and a deleted stub answers with a 301 to
  the target.
- **HubSpot** answers a request for a merged record's old id with the merged record. The
  survivor's `hs_merged_object_ids` property holds each old id.
- **slate's fold must follow these three.** Every row on the folded idea's number moves to the
  record that carries the work. The idea's number is kept in a redirect, as Jira's
  `moved_issue_key` keeps an old key. The survivor's read lists every number that leads to it, as
  HubSpot's `hs_merged_object_ids` does. That list is where a wrong fold shows.

## An old id answers with its record under a 200, because a redirect breaks the callers that matter

- **GitHub's REST API answers a transferred issue with a 301**, and its docs tell clients to follow
  redirects.
- **Jira's REST API never answered a moved key with a redirect.** Scripts broke where browsers did
  not.
- **HubSpot answers an old id with the survivor under a 200.**
- **slate must answer with the target under a 200, under the target's own number.** A kind's route
  that a 308 sent elsewhere would answer another kind's schema. slate's callers, the CLI and an
  importer that copies records into a warehouse, decode one schema per route. A changed number in the answer tells a
  caller that a redirect was followed. `GET /records/{number}` also names it in `redirected_from`.

## Measurements

Taken 2026-11-24 against slate's API.

| Command | Result |
| --- | --- |
| `slate ideas list --json \| jq '[.[] \| select(.initiative)] \| length'` | 12 of 28 |
| `slate worklogs list --record <n> --state all --json \| jq length`, summed over the promoted ideas | 8, each a shape step |
| `slate show <n> --json \| jq '.record.claims \| length'`, summed over the promoted ideas | 8, all ended |
| `slate show <n> --json \| jq '.record.comments \| length'`, summed over the promoted ideas | 1 |
| The same reads of refs, tags and pursuits on the promoted ideas | 3, 4 and 6, each copied to its initiative at promotion |

## Sources

| Source | Read at | What it gave |
| --- | --- | --- |
| [Merging Records](https://developer.salesforce.com/docs/atlas.en-us.apexcode.meta/apexref/langCon_apex_dml_examples_merge.htm), Salesforce | 2026-11-24, as a search summary | losers deleted, related records reparented, `MasterRecordId` on each loser |
| [Improved question merging](https://stackoverflow.blog/2010/06/10/improved-question-merging/), Stack Overflow blog | 2026-11-24, as a search summary | answers move to the target, the source stays a stub, a deleted stub answers 301 |
| [Updated merge functionality for contacts and companies](https://developers.hubspot.com/changelog/updated-merge-functionality-for-contacts-and-companies), HubSpot | 2026-11-24, as a search summary | an old id answers with the merged record, and `hs_merged_object_ids` lists the old ids |
| GitHub REST API docs on getting an issue | 2026-11-24, as a search summary | a transferred issue answers 301, and clients follow redirects |
| [Moved issues no longer redirect from previous issue key](https://support.atlassian.com/jira/kb/moved-issues-no-longer-redirect-from-previous-issue-key-or-url-in-jira/) and [JRA-26806](https://jira.atlassian.com/browse/JRA-26806), Atlassian | 2026-11-24, as search summaries | `moved_issue_key`, and REST callers getting no redirect |
| [Move and change work item type](https://learn.microsoft.com/en-us/azure/devops/boards/backlogs/move-change-type), Microsoft | 2026-11-24, as a search summary | a type change keeps the item and resets its state |

## What was searched

- Earlier research notes for merged records, tombstones, record redirects and 301 answers. None
  held them.
- The web for Salesforce's, HubSpot's and Stack Overflow's merges, and GitHub's answer to a
  transferred issue. Each page was read through a search summary, not opened.
