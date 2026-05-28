package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/store"
)

type StorePushDataCommand struct {
	Data PushData `json:"data"`
}

type notificationEvent struct {
	name string
	data PushData
}

func (s *Service) StoreNotificationPushData(ctx context.Context, tenantID string, cmd StorePushDataCommand) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	data := cmd.Data
	data.UUID = strings.TrimSpace(data.UUID)
	if data.UUID == "" {
		return ErrMissingUUID
	}
	data.TenantID = tenantID
	data.UserMobile = strings.TrimSpace(data.UserMobile)
	data.Phone = strings.TrimSpace(data.Phone)
	return s.Store.CreatePushData(ctx, tenantID, &data)
}

func (s *Service) StorePushDataInNotificationChat(ctx context.Context, tenantID string, data PushData) error {
	return s.doAdminServiceCommand(ctx, tenantID, notificationCommandTarget, "/internal/notification-chat/push-data", StorePushDataCommand{Data: data}, nil)
}

func (s *Service) StoreNotificationEventsInNotificationChat(ctx context.Context, tenantID string, events ...notificationEvent) error {
	var joined error
	for _, event := range events {
		data, err := notificationRecordForEvent(event.data, event.name)
		if err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		if err := s.StorePushDataInNotificationChat(ctx, tenantID, data); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func notificationRecordForEvent(data PushData, event string) (PushData, error) {
	transactionUUID := strings.TrimSpace(data.UUID)
	if transactionUUID == "" {
		return PushData{}, ErrMissingUUID
	}
	event = strings.TrimSpace(event)
	if event == "" {
		return PushData{}, ErrMissingUUID
	}
	data.EBSUUID = transactionUUID
	data.UUID = transactionUUID + ":" + event
	return data, nil
}
