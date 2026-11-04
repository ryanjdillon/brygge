# Importing harbor share owners

> Audience: the site admin moving the club's harbor share (moloandel) register into Brygge. Who owns a share is a field on the member: **Moloandel** on the Members page, with an optional paid date.

The import reads the club's member sheet and marks the members who own a share. It only updates members already in Brygge: create missing members first with **Importer CSV**, then run this import again.

## What the import does and never does

- It marks a member as owner, and fills in a paid date where the member has none.
- It **never** overwrites a paid date that is already recorded, and **never** removes ownership. A row that disagrees with Brygge is shown to you and left alone; correct the member by hand if the sheet is right.
- Nothing changes until you press **Bruk endringer**. The preview before it is exactly what will be written.

## The file

Save the sheet from Excel or Numbers as **CSV UTF-8** (in Excel: *File → Save as → CSV UTF-8 (comma delimited)*). Plain "CSV" from a Norwegian Excel is not UTF-8, and Brygge refuses it, because æ, ø and å would come out wrong and no name with them would match. Columns may be separated by commas or semicolons.

The first row holds the column names:

| Column | Required | What goes in it |
|---|---|---|
| `name` | one of `name`, `first_name` + `last_name`, or `email` | The member's full name, as in Brygge. Case and extra spaces don't matter. |
| `first_name`, `last_name` | (instead of `name`) | First and last name in two columns. |
| `email` | | When given and found, the member is matched on it, even if the name differs. |
| `harbor_share_owner` | yes | `ja`, `yes`, `x`, `1` or `true` for an owner; `nei`, `no`, `0`, `false` or empty for not an owner. |
| `harbor_share_paid_on` | no | The date the share was paid: `2026-03-15` or `15.03.2026`. Leave it empty if you don't know it; Brygge never makes one up. |

Example:

```
name;email;harbor_share_owner;harbor_share_paid_on
Ola Nordmann;;ja;
Kari Nordmann;kari@example.com;ja;15.03.2026
Per Hansen;;nei;
```

## Running it

1. On **Medlemmer**, press **Importer moloandeler**. You are asked to confirm with your authenticator first.
2. Choose the file and press **Forhåndsvis**.
3. Read the preview. Rows are grouped by what will happen to them:
   - **Blir eier** and **Betalingsdato lagt til** are the changes.
   - **Flere medlemmer passer**: more than one member has that name. Pick the right one; their slips are shown to tell them apart.
   - **Fant ikke medlemmet**: search for the member, or create them with the member import and start again.
   - **Annen dato registrert** and **Eier i Brygge, ikke i filen** are left alone. Check them by hand.
   - **Samme medlem på flere rader**: rows that name the same member with different values are all left out.
   - **Raden har en feil**: an unreadable date or owner value. Fix the file and upload again.
   - Deactivated members are marked as owners too, since they still own the share; the row says they are deactivated.
4. Press **Bruk endringer (N)**.

If someone edits a member while you have the preview open, nothing is written: the import shows a fresh preview for you to check and apply again.
