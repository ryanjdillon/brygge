DROP TABLE IF EXISTS shopping_list_items;
DROP TABLE IF EXISTS shopping_lists;
DROP TABLE IF EXISTS task_participants;
DROP TABLE IF EXISTS project_events;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS estimated_hours,
    DROP COLUMN IF EXISTS actual_hours,
    DROP COLUMN IF EXISTS ansvarlig_id,
    DROP COLUMN IF EXISTS max_collaborators,
    DROP COLUMN IF EXISTS materials;

ALTER TABLE clubs
    DROP COLUMN IF EXISTS required_dugnad_hours;
