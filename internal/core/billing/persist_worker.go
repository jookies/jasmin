package billing

import (
	"context"
	"time"
)

type PersistWorker struct {
	userRepo  UserRepository
	groupRepo GroupRepository
	interval  time.Duration
	users     func() []*User
	groups    func() []*Group
}

func NewPersistWorker(userRepo UserRepository, groupRepo GroupRepository, interval time.Duration, users func() []*User, groups func() []*Group) *PersistWorker {
	return &PersistWorker{
		userRepo:  userRepo,
		groupRepo: groupRepo,
		interval:  interval,
		users:     users,
		groups:    groups,
	}
}

func (w *PersistWorker) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.PersistAll(ctx); err != nil {
				// In a real app we'd log the error
			}
		}
	}
}

func (w *PersistWorker) PersistAll(ctx context.Context) error {
	for _, g := range w.groups() {
		if err := w.groupRepo.Save(ctx, g); err != nil {
			return err
		}
	}
	for _, u := range w.users() {
		if err := w.userRepo.Save(ctx, u); err != nil {
			return err
		}
	}
	return nil
}
