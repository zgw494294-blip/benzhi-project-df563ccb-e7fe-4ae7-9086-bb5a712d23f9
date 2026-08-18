package application

import (
	"context"
	"time"

	"stallsettle/internal/domain"
)

type Ledger interface {
	Load(context.Context) (domain.LedgerSnapshot, error)
	Save(context.Context, domain.LedgerSnapshot) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}
