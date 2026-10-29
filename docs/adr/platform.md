# Platform decisions

Cross-cutting product rules. See [README.md](README.md) for the format.

### PLAT-1: Every feature is configurable per club, with no defaults copied from one club

- **Status:** accepted · 2026-10-16 · BRY-351
- **Decision:** Brygge is built to be deployed by any club. A choice that differs between clubs is per-club configuration that starts empty (or at a neutral default), never a seeded value copied from the reference club's practice. Where it's cheap, the configuration is made general (role × any price item, rather than "free slip for the board").
- **Consequences:** a fix that comes out of one club's data is split into a general feature and a separate operator step for that club.

### PLAT-2: Every view works on phones and tablets (ratchet)

- **Status:** accepted · 2026-10-28 · BRY-464
- **Decision:** every view that is added or changed must work at 375px, 768px and desktop widths: no horizontal page scroll, wide tables become cards on small screens, touch targets of at least 44px, single-column forms and full-screen dialogs on phones. A view that is touched is left no worse on mobile than it was. Remaining gaps are tracked on BRY-464.
- **Why:** board members do much of their work from a phone, at the harbour.
- **Consequences:** the detailed rule is in `AGENTS.md` under Code Conventions.
