package db

import (
	"context"
	"testing"
)

// TestReplaceChunksForMaterialAssignsEntryID is a regression test: the upload
// and reindex paths build chunks without a knowledge_entry_id, and the storage
// layer is responsible for assigning it. A missing assignment fails the
// foreign key and the whole replacement.
func TestReplaceChunksForMaterialAssignsEntryID(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-切片归属-A", "正文用于重建")

	// Exactly what the HTTP layer passes: spans and text, no identifiers.
	replacement := []Chunk{
		{ChunkIndex: 0, CharStart: 0, CharEnd: 3, ChunkText: "正文用"},
		{ChunkIndex: 1, CharStart: 3, CharEnd: 5, ChunkText: "于重建"},
	}
	if err := ReplaceChunksForMaterial(ctx, conn, matA, classA, "custom", nil, replacement); err != nil {
		t.Fatalf("ReplaceChunksForMaterial failed: %v", err)
	}

	stored, err := ListChunksByMaterial(ctx, conn, matA, classA, "")
	if err != nil {
		t.Fatalf("list replaced chunks: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("got %d chunks, want 2", len(stored))
	}
	for _, c := range stored {
		if c.KnowledgeEntryID != entryA {
			t.Fatalf("chunk %d has knowledge_entry_id %d, want %d", c.ID, c.KnowledgeEntryID, entryA)
		}
		if c.MaterialID != matA {
			t.Fatalf("chunk %d has material_id %d, want %d", c.ID, c.MaterialID, matA)
		}
		if c.ClassID != classA {
			t.Fatalf("chunk %d has class_id %d, want %d", c.ID, c.ClassID, classA)
		}
		if c.IndexStatus != IndexPending {
			t.Fatalf("chunk %d status = %q, want pending", c.ID, c.IndexStatus)
		}
	}
}
