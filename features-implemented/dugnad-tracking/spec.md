# Dugnad Attendance & Task Tracking

## Overview

Extend the existing project/kanban system to support dugnad events with task sign-up,
hour tracking, material/cost management, and a shopping list aggregation view.

## Core Concepts

### Dugnad Event Link
- A dugnad calendar event can be linked to one or more projects
- Projects can be created inline when setting up a dugnad, or existing projects linked
- Admin dugnad view shows all linked projects with task summary

### Task Enhancements

Extend existing project tasks with:

| Field              | Type       | Description                                    |
|--------------------|------------|------------------------------------------------|
| `estimated_hours`  | NUMERIC    | Estimated time to complete                     |
| `actual_hours`     | NUMERIC    | Actual hours (set by styre post-completion)     |
| `ansvarlig_id`     | UUID       | Required responsible person (first to join)     |
| `max_collaborators`| INT        | Max number of additional participants           |
| `materials`        | JSONB      | Array of {item, quantity, unit, est_cost}       |
| `priority`         | TEXT       | `high`, `medium`, `low`                        |

### Task Participation

- Members can "bli med" a task, filling a collaborator slot
- First person to join becomes `ansvarlig` automatically
- `ansvarlig` can be reassigned by styre or current participants
- Styre can also assign members to tasks directly
- Participants stored in `task_participants` join table

### Hour Tracking

- When a task is marked complete, `actual_hours` is set
- Hours are split equally among participants by default
- Styre can retroactively adjust individual participant hours
- Each member's profile shows:
  - **Signed up hours** — sum of estimated_hours for tasks they've joined (incomplete)
  - **Completed hours** — sum of their actual_hours for completed tasks
  - **Required hours** — club-wide setting (e.g., 20h/year), configurable by styre
  - **Remaining** — required minus completed (can be negative = surplus)

### Materials & Shopping Lists

Each task can have a materials list:
```json
[
  {"item": "Beis (hvit)", "quantity": 5, "unit": "liter", "est_cost": 250},
  {"item": "Pensler", "quantity": 10, "unit": "stk", "est_cost": 50}
]
```

**Shopping Lists** are a separate entity:
- Title, description, created_by, status (active/completed)
- Items from task materials can be added to a shopping list
- A shopping list can aggregate materials from:
  - Selected individual tasks
  - All tasks in a project
  - All tasks linked to a dugnad event
- Shopping list view:
  - Groupable by task or flat list
  - Toggle to show/hide task titles (for when sharing with a member doing the buying)
  - Printable (clean print CSS)
  - Accessible from app (responsive, works in PWA)
- Styre can share a shopping list with a specific member (read-only link or assignment)

## Schema Changes

```sql
-- Extend existing tasks table
ALTER TABLE tasks
    ADD COLUMN estimated_hours  NUMERIC,
    ADD COLUMN actual_hours     NUMERIC,
    ADD COLUMN ansvarlig_id     UUID REFERENCES users(id),
    ADD COLUMN max_collaborators INT NOT NULL DEFAULT 5,
    ADD COLUMN materials        JSONB NOT NULL DEFAULT '[]';

-- Link projects to dugnad events
CREATE TABLE project_events (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    event_id    UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, event_id)
);

-- Task participation
CREATE TABLE task_participants (
    task_id     UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'collaborator',  -- 'ansvarlig' or 'collaborator'
    hours       NUMERIC,  -- actual hours for this participant (nullable until completed)
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, user_id)
);

-- Club dugnad hour requirement
ALTER TABLE clubs
    ADD COLUMN required_dugnad_hours NUMERIC NOT NULL DEFAULT 0;

-- Shopping lists
CREATE TABLE shopping_lists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by  UUID NOT NULL REFERENCES users(id),
    shared_with UUID REFERENCES users(id),
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE shopping_list_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    list_id         UUID NOT NULL REFERENCES shopping_lists(id) ON DELETE CASCADE,
    task_id         UUID REFERENCES tasks(id) ON DELETE SET NULL,
    item            TEXT NOT NULL,
    quantity        NUMERIC,
    unit            TEXT NOT NULL DEFAULT '',
    est_cost        NUMERIC,
    checked         BOOLEAN NOT NULL DEFAULT false,
    sort_order      INT NOT NULL DEFAULT 0
);
```

## API Endpoints

### Task Participation
- `POST /api/v1/projects/{projectID}/tasks/{taskID}/join` — join a task
- `DELETE /api/v1/projects/{projectID}/tasks/{taskID}/leave` — leave a task
- `PUT /api/v1/admin/projects/{projectID}/tasks/{taskID}/assign` — assign member
- `PUT /api/v1/admin/projects/{projectID}/tasks/{taskID}/hours` — adjust hours

### Dugnad Hours
- `GET /api/v1/members/me/dugnad-hours` — own hour summary
- `GET /api/v1/admin/users/dugnad-hours` — all members' hours (filterable/sortable)
- `PUT /api/v1/admin/settings/dugnad-hours` — set required hours

### Shopping Lists
- `GET /api/v1/shopping-lists` — list all (styre sees all, members see shared)
- `POST /api/v1/shopping-lists` — create new list
- `PUT /api/v1/shopping-lists/{id}` — update list
- `DELETE /api/v1/shopping-lists/{id}` — delete list
- `POST /api/v1/shopping-lists/{id}/from-tasks` — populate from task materials
- `GET /api/v1/shopping-lists/{id}/print` — printable view

### Project-Event Link
- `POST /api/v1/admin/events/{eventID}/projects` — link project to event
- `DELETE /api/v1/admin/events/{eventID}/projects/{projectID}` — unlink

## Admin Views

- **Users list**: add columns for signed-up hours, completed hours, remaining hours
  with sort/filter capability
- **Dugnad event detail**: show linked projects, task summary, participant counts
- **Settings**: required dugnad hours per year

## Portal Views

- **Profile/dashboard**: dugnad hour summary card (signed up / completed / remaining)
- **Project tasks**: "Bli med" button, participant avatars, materials preview
- **Shopping list**: list view with checkbox items, print button

## Sort Options for Tasks in a Project

- Priority (high → low)
- Total estimated cost (materials sum)
- Estimated time
- Manual (drag-and-drop, stored as sort_order)

## Future

- Integration with external list services (Todoist, etc.) via webhooks or API
