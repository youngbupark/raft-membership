package fsm

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/raft"
)

// FSM implements a finite state machine that is used
// along with Raft to provide strong consistency. We implement
// this outside the Server to avoid exposing this outside the package.
type FSM struct {
	// memershipLock is only used to protect outside callers to State() from
	// racing with Restore(), which is called by Raft (it puts in a totally
	// new state store). Everything internal here is synchronized by the
	// Raft side, so doesn't need to lock this.
	memershipLock sync.RWMutex
	memership     *HostMembership
}

func New() *FSM {
	return &FSM{
		memership: &HostMembership{Ver: "0", Members: []HostMember{}},
	}
}

// State is used to return a handle to the current state
func (c *FSM) State() *HostMembership {
	c.memershipLock.RLock()
	defer c.memershipLock.RUnlock()
	return c.memership
}

func (c *FSM) Apply(log *raft.Log) interface{} {
	m := &HostMembership{}

	if err := json.Unmarshal(log.Data, m); err != nil {
		panic(fmt.Sprintf("failed to unmarshal command: %s", err.Error()))
	}

	c.memershipLock.Lock()
	c.memership = m
	c.memershipLock.Unlock()

	return nil
}

func (c *FSM) Snapshot() (raft.FSMSnapshot, error) {
	return &snapshot{
		state: c.memership.Clone(),
	}, nil
}

// Restore streams in the snapshot and replaces the current state store with a
// new one based on the snapshot if all goes OK during the restore.
func (c *FSM) Restore(old io.ReadCloser) error {
	defer old.Close()

	m, err := c.memership.Decode(old)
	if err != nil {
		return err
	}

	c.memershipLock.Lock()
	c.memership = m
	c.memershipLock.Unlock()

	return nil
}
