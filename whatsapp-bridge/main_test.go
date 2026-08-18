package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *MessageStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);
		CREATE TABLE messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	if err := ensureEphemeralColumn(db); err != nil {
		t.Fatalf("ensureEphemeralColumn: %v", err)
	}
	return &MessageStore{db: db}
}

func TestEnsureEphemeralColumnIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	// A pre-migration store (no ephemeral_seconds) is exactly what newTestStore builds --
	// calling it again (as NewMessageStore does on every startup) must not error.
	if err := ensureEphemeralColumn(store.db); err != nil {
		t.Fatalf("second ensureEphemeralColumn call: %v", err)
	}
}

func TestStoreChatPreservesEphemeralAcrossUpdates(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if err := store.StoreChat("g1@g.us", "Racing", time.Now()); err != nil {
		t.Fatalf("StoreChat: %v", err)
	}
	if err := store.SetChatEphemeral("g1@g.us", 86400); err != nil {
		t.Fatalf("SetChatEphemeral: %v", err)
	}

	// A later message in the same chat re-calls StoreChat (INSERT OR REPLACE's old
	// behavior would have wiped ephemeral_seconds back to NULL here).
	if err := store.StoreChat("g1@g.us", "Racing", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("second StoreChat: %v", err)
	}

	var seconds sql.NullInt64
	if err := store.db.QueryRow("SELECT ephemeral_seconds FROM chats WHERE jid = ?", "g1@g.us").Scan(&seconds); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !seconds.Valid || seconds.Int64 != 86400 {
		t.Fatalf("ephemeral_seconds not preserved: valid=%v value=%v", seconds.Valid, seconds.Int64)
	}
}

func TestEphemeralKnownDistinguishesUnresolvedFromNotEphemeral(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if err := store.StoreChat("g1@g.us", "Racing", time.Now()); err != nil {
		t.Fatalf("StoreChat: %v", err)
	}
	known, err := store.EphemeralKnown("g1@g.us")
	if err != nil {
		t.Fatalf("EphemeralKnown: %v", err)
	}
	if known {
		t.Fatalf("expected unresolved chat to report known=false")
	}

	if err := store.SetChatEphemeral("g1@g.us", 0); err != nil {
		t.Fatalf("SetChatEphemeral: %v", err)
	}
	known, err = store.EphemeralKnown("g1@g.us")
	if err != nil {
		t.Fatalf("EphemeralKnown: %v", err)
	}
	if !known {
		t.Fatalf("expected resolved-not-ephemeral chat to report known=true")
	}
}

func TestPruneOldMessagesRespectsPerChatEphemeralCeiling(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	now := time.Now()
	mustStoreChat := func(jid string) {
		t.Helper()
		if err := store.StoreChat(jid, jid, now); err != nil {
			t.Fatalf("StoreChat(%s): %v", jid, err)
		}
	}
	mustStoreMessage := func(jid, id string, age time.Duration) {
		t.Helper()
		if err := store.StoreMessage(id, jid, "sender", "hi", now.Add(-age), false, "", "", "", nil, nil, nil, 0); err != nil {
			t.Fatalf("StoreMessage(%s): %v", jid, err)
		}
	}

	// short@g.us: WhatsApp's own 24h disappearing timer -- shorter than our
	// messageRetentionDays (30d) ceiling, so it should win and prune the 2-day-old row.
	mustStoreChat("short@g.us")
	if err := store.SetChatEphemeral("short@g.us", 24*60*60); err != nil {
		t.Fatalf("SetChatEphemeral: %v", err)
	}
	mustStoreMessage("short@g.us", "m1", 2*24*time.Hour)

	// long@g.us: a 90-day disappearing timer -- our ceiling should win instead, so a
	// 2-day-old message here must survive.
	mustStoreChat("long@g.us")
	if err := store.SetChatEphemeral("long@g.us", 90*24*60*60); err != nil {
		t.Fatalf("SetChatEphemeral: %v", err)
	}
	mustStoreMessage("long@g.us", "m2", 2*24*time.Hour)

	// unresolved@g.us: no ephemeral timer known at all -- falls back to the flat
	// ceiling, so a 2-day-old message survives too.
	mustStoreChat("unresolved@g.us")
	mustStoreMessage("unresolved@g.us", "m3", 2*24*time.Hour)

	deleted, err := store.PruneOldMessages()
	if err != nil {
		t.Fatalf("PruneOldMessages: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected exactly 1 row pruned (the 24h-ephemeral chat's message), got %d", deleted)
	}

	assertCount := func(jid string, want int) {
		t.Helper()
		var got int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM messages WHERE chat_jid = ?", jid).Scan(&got); err != nil {
			t.Fatalf("count(%s): %v", jid, err)
		}
		if got != want {
			t.Fatalf("chat_jid=%s: want %d messages, got %d", jid, want, got)
		}
	}
	assertCount("short@g.us", 0)
	assertCount("long@g.us", 1)
	assertCount("unresolved@g.us", 1)
}
