package db

// ImageTaskQueryIndexesSQL follows accepted order for owner history and keeps
// admission counts on the bounded set of unfinished financial reservations.
const ImageTaskQueryIndexesSQL = `DROP INDEX idx_image_activity_tasks_user;
CREATE INDEX idx_image_activity_tasks_user ON image_activity_tasks(user_id,accepted_seq,completed_at);
DROP INDEX idx_image_activity_tasks_unfinished;
CREATE INDEX idx_image_activity_tasks_unfinished ON image_activity_tasks(user_id,accepted_seq) WHERE finance_state='reserved';
`
