package fsm

import (
	"encoding/json"
	"io"
	"time"
)

type HostMember struct {
	Name      string    `json:"name"`
	Desc      string    `json:"description"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type HostMembership struct {
	Ver     string       `json:"ver"`
	Members []HostMember `json:"members"`
}

func (h *HostMembership) Clone() *HostMembership {
	newHost := &HostMembership{Ver: h.Ver}
	copy(newHost.Members, h.Members)
	return newHost
}

func (h *HostMembership) Decode(r io.Reader) (*HostMembership, error) {
	m := &HostMembership{}
	if err := json.NewDecoder(r).Decode(m); err != nil {
		return nil, err
	}
	return m, nil
}
