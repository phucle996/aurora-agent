package adminrpc

import (
	runtimev1 "github.com/phucle996/aurora-proto/runtimev1"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

type hostRoutingEntry struct {
	Host    string
	Address string
}

func (c *HeartbeatClient) maybeSyncHostRouting(ctx context.Context, agentID string) error {
	if c == nil {
		return fmt.Errorf("heartbeat client is nil")
	}
	now := time.Now().UTC()

	c.hostRoutingMu.Lock()
	if !c.lastHostSyncAt.IsZero() && now.Sub(c.lastHostSyncAt) < hostRoutingSyncInterval {
		c.hostRoutingMu.Unlock()
		return nil
	}
	c.lastHostSyncAt = now
	c.hostRoutingMu.Unlock()

	return c.syncHostRoutingSnapshot(ctx, agentID)
}

func (c *HeartbeatClient) syncHostRoutingSnapshot(ctx context.Context, agentID string) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("heartbeat client is nil")
	}

	callCtx, cancel := context.WithTimeout(ctx, defaultInvokeTimeout)
	defer cancel()

	var resp *runtimev1.GetHostRoutingSnapshotResponse
	if err := c.invokeWithRecovery(callCtx, func(client runtimev1.RuntimeServiceClient) error {
		var callErr error
		resp, callErr = client.GetHostRoutingSnapshot(callCtx, &runtimev1.GetHostRoutingSnapshotRequest{
			AgentId: strings.TrimSpace(agentID),
		})
		return callErr
	}); err != nil {
		return err
	}

	entries := readHostRoutingEntries(resp)
	snapshotHash := computeHostRoutingSnapshotHash(entries)

	c.hostRoutingMu.Lock()
	if snapshotHash == c.lastHostSyncHash {
		c.hostRoutingMu.Unlock()
		return nil
	}
	c.hostRoutingMu.Unlock()

	if err := applyHostRoutingSnapshot(entries); err != nil {
		return err
	}

	c.hostRoutingMu.Lock()
	c.lastHostSyncHash = snapshotHash
	c.hostRoutingMu.Unlock()
	return nil
}

func readHostRoutingEntries(resp *runtimev1.GetHostRoutingSnapshotResponse) []hostRoutingEntry {
	if resp == nil {
		return nil
	}
	items := resp.GetItems()
	out := make([]hostRoutingEntry, 0, len(items))
	for _, value := range items {
		if value == nil {
			continue
		}
		host := strings.TrimSpace(value.GetHost())
		address := strings.TrimSpace(value.GetAddress())
		if host == "" || address == "" {
			continue
		}
		out = append(out, hostRoutingEntry{Host: host, Address: address})
	}
	return out
}

func computeHostRoutingSnapshotHash(entries []hostRoutingEntry) string {
	if len(entries) == 0 {
		return "empty"
	}
	normalized := make([]string, 0, len(entries))
	for _, entry := range entries {
		host := strings.TrimSpace(entry.Host)
		address := strings.TrimSpace(entry.Address)
		if host == "" || address == "" {
			continue
		}
		normalized = append(normalized, host+"="+address)
	}
	if len(normalized) == 0 {
		return "empty"
	}
	slices.Sort(normalized)
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\n")))
	return fmt.Sprintf("%x", sum[:])
}

func applyHostRoutingSnapshot(entries []hostRoutingEntry) error {
	const beginMarker = "# BEGIN AURORA HOST ROUTING"
	const endMarker = "# END AURORA HOST ROUTING"
	const hostsFile = "/etc/hosts"

	sortedEntries := append([]hostRoutingEntry(nil), entries...)
	slices.SortFunc(sortedEntries, func(a, b hostRoutingEntry) int {
		if a.Host == b.Host {
			return strings.Compare(a.Address, b.Address)
		}
		return strings.Compare(a.Host, b.Host)
	})

	blockLines := make([]string, 0, len(sortedEntries)+2)
	blockLines = append(blockLines, beginMarker)
	for _, entry := range sortedEntries {
		host := strings.TrimSpace(entry.Host)
		address := strings.TrimSpace(entry.Address)
		if host == "" || address == "" {
			continue
		}
		blockLines = append(blockLines, address+" "+host)
	}
	blockLines = append(blockLines, endMarker)
	block := strings.Join(blockLines, "\n")

	current, err := os.ReadFile(hostsFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read /etc/hosts failed: %w", err)
	}

	base := stripManagedHostsBlock(string(current), beginMarker, endMarker)
	base = strings.TrimRight(base, "\n")
	content := block + "\n"
	if base != "" {
		content = base + "\n\n" + block + "\n"
	}

	tmpPath := hostsFile + ".aurora.tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write temporary hosts file failed: %w", err)
	}
	if err := os.Rename(tmpPath, hostsFile); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace /etc/hosts failed: %w", err)
	}
	return nil
}

func stripManagedHostsBlock(content string, beginMarker string, endMarker string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	begin := strings.Index(normalized, beginMarker)
	end := strings.Index(normalized, endMarker)
	if begin < 0 || end < 0 || end < begin {
		return normalized
	}
	end += len(endMarker)
	if end < len(normalized) && normalized[end] == '\n' {
		end++
	}
	out := normalized[:begin] + normalized[end:]
	return strings.Trim(out, "\n")
}
