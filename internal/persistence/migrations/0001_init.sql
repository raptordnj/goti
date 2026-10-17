CREATE TABLE tasks (
	id TEXT PRIMARY KEY,
	original_url TEXT NOT NULL,
	final_url TEXT NOT NULL,
	dest_dir TEXT NOT NULL,
	file_name TEXT NOT NULL,
	temp_path TEXT NOT NULL,
	size INTEGER NOT NULL,
	size_known INTEGER NOT NULL,
	etag TEXT NOT NULL,
	etag_strong INTEGER NOT NULL,
	last_modified TEXT NOT NULL,
	strategy TEXT NOT NULL,
	decision_json BLOB,
	state TEXT NOT NULL,
	last_error_kind INTEGER NOT NULL,
	last_error_msg TEXT NOT NULL,
	expected_sha256 TEXT NOT NULL,
	existing_policy TEXT NOT NULL,
	settings_json BLOB,
	final_path TEXT NOT NULL,
	verify_result TEXT NOT NULL,
	downloaded INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX tasks_created ON tasks(created_at);
CREATE TABLE segments (
	task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	start INTEGER NOT NULL,
	end INTEGER NOT NULL,
	pos INTEGER NOT NULL,
	done INTEGER NOT NULL,
	hash BLOB,
	hasher_state BLOB,
	PRIMARY KEY (task_id, start)
);
CREATE TABLE attempts (
	id INTEGER PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	at INTEGER NOT NULL,
	kind INTEGER NOT NULL,
	message TEXT NOT NULL,
	delay_ms INTEGER NOT NULL
);
CREATE INDEX attempts_task ON attempts(task_id, id);
