package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAskMemberPoolIsAtomic (H-9): sixteen members asking at once with one pool
// slot left get exactly one answer and the refused hand their slot back; a
// member refused at their own cap never takes a slot.
func TestAskMemberPoolIsAtomic(t *testing.T) {
	fake := &statsLLM{echoLLM: echoLLM{plan: planFor(`{"query":"prereg_chain","params":{}}`)}}
	d, _, _ := privateAskServer(t, fake)
	d.Cfg.MemberCopilot = true
	ctx := context.Background()
	day := time.Now().UTC().Format("2006-01-02")

	var uids []int64
	for i := 0; i < 16; i++ {
		uid, err := d.St.CreateUser(ctx, fmt.Sprintf("pool%02d", i), "x", false)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		uids = append(uids, uid)
	}

	poolKey := "copilot_ask:" + day + ":0"
	if err := d.St.SetMeta(ctx, poolKey, fmt.Sprint(askMemberPool-1)); err != nil {
		t.Fatalf("set pool meta: %v", err)
	}

	codes := make([]int, 16)
	bodies := make([]string, 16)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, uid := range uids {
		wg.Add(1)
		go func(i int, uid int64) {
			defer wg.Done()
			<-start
			code, body := askDirect(t, d, uid, "pool?")
			codes[i] = code
			bodies[i] = body
		}(i, uid)
	}
	close(start)
	wg.Wait()

	successCount := 0
	for i, code := range codes {
		if code == 200 {
			successCount++
		} else if code != 429 || !strings.Contains(bodies[i], "member questions are used up") {
			t.Errorf("unexpected response from user %d: code=%d body=%q", uids[i], code, bodies[i])
		}
	}
	if successCount != 1 {
		t.Errorf("expected exactly one 200, got %d; codes=%v", successCount, codes)
	}

	if got, err := d.St.AskCount(ctx, askMemberPoolUID, day); err != nil {
		t.Fatalf("AskCount: %v", err)
	} else if got != askMemberPool {
		t.Errorf("pool ask count after first phase: got %d, want %d", got, askMemberPool)
	}

	if err := d.St.SetMeta(ctx, poolKey, "0"); err != nil {
		t.Fatalf("reset pool meta: %v", err)
	}
	u := uids[0]
	userKey := "copilot_ask:" + day + ":" + fmt.Sprint(u)
	if err := d.St.SetMeta(ctx, userKey, fmt.Sprint(askCapMember)); err != nil {
		t.Fatalf("set user meta: %v", err)
	}
	code, body := askDirect(t, d, u, "pool?")
	if code != 429 || !strings.Contains(body, "used today's questions") {
		t.Errorf("second phase: expected 429 with 'used today\\'s questions', got %d %q", code, body)
	}
	if got, err := d.St.AskCount(ctx, askMemberPoolUID, day); err != nil {
		t.Fatalf("AskCount second: %v", err)
	} else if got != 0 {
		t.Errorf("pool ask count after second phase: got %d, want 0", got)
	}
}