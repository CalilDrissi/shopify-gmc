package store

import (
	"testing"

	"github.com/google/uuid"
)

func TestGmcAccountSnapshotsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	st := seedStore(ctx, t, tx, tn.ID)

	conn := &StoreGmcConnection{StoreID: st.ID, MerchantID: "MC-99"}
	if err := (StoreGmcConnectionsRepo{}).Insert(ctx, tx, tn.ID, conn); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	pc := 100
	for i := 0; i < 3; i++ {
		s := &GmcAccountSnapshot{
			GmcConnectionID: conn.ID,
			ProductCount:    &pc,
			RawData:         []byte(`{"products":1}`),
		}
		if err := (GmcAccountSnapshotsRepo{}).Insert(ctx, tx, tn.ID, s); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	list, err := (GmcAccountSnapshotsRepo{}).ListByConnection(ctx, tx, tn.ID, conn.ID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("len=%d want 3", len(list))
	}

	other := uuid.New()
	otherList, err := (GmcAccountSnapshotsRepo{}).ListByConnection(ctx, tx, other, conn.ID, 10)
	if err != nil {
		t.Fatalf("cross-tenant List: %v", err)
	}
	if len(otherList) != 0 {
		t.Errorf("cross-tenant len=%d want 0", len(otherList))
	}
}

func TestGmcProductStatusesRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	st := seedStore(ctx, t, tx, tn.ID)

	cases := []struct {
		name, productID, status string
	}{
		{"approved-1", "shopify://product/1", "approved"},
		{"disapproved-1", "shopify://product/2", "disapproved"},
		{"pending-1", "shopify://product/3", "pending"},
		{"approved-2", "shopify://product/4", "approved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &GmcProductStatus{
				StoreID:        st.ID,
				ProductID:      tc.productID,
				ApprovalStatus: tc.status,
			}
			if err := (GmcProductStatusesRepo{}).Upsert(ctx, tx, tn.ID, p); err != nil {
				t.Fatalf("Upsert: %v", err)
			}
		})
	}

	t.Run("Idempotent upsert", func(t *testing.T) {
		p := &GmcProductStatus{
			StoreID:        st.ID,
			ProductID:      "shopify://product/1",
			ApprovalStatus: "disapproved",
		}
		if err := (GmcProductStatusesRepo{}).Upsert(ctx, tx, tn.ID, p); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	})

	list, err := (GmcProductStatusesRepo{}).ListByStore(ctx, tx, tn.ID, st.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 4 {
		t.Errorf("len=%d want 4", len(list))
	}

	counts, err := (GmcProductStatusesRepo{}).CountByApproval(ctx, tx, tn.ID, st.ID)
	if err != nil {
		t.Fatalf("CountByApproval: %v", err)
	}
	if counts["disapproved"] != 2 {
		t.Errorf("disapproved=%d want 2 (after upsert)", counts["disapproved"])
	}
	if counts["approved"] != 1 {
		t.Errorf("approved=%d want 1", counts["approved"])
	}
	if counts["pending"] != 1 {
		t.Errorf("pending=%d want 1", counts["pending"])
	}

	otherTenant := uuid.New()
	otherList, err := (GmcProductStatusesRepo{}).ListByStore(ctx, tx, otherTenant, st.ID)
	if err != nil {
		t.Fatalf("cross-tenant List: %v", err)
	}
	if len(otherList) != 0 {
		t.Errorf("cross-tenant len=%d want 0", len(otherList))
	}
}
