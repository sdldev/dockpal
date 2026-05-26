package db

import (
	"encoding/json"

	"go.etcd.io/bbolt"
)

// NotificationWebhook represents an outgoing webhook endpoint that receives
// notifications about app update events (rolled_back, failed, etc.).
type NotificationWebhook struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	CreatedAt int64  `json:"created_at"`
}

// CreateNotificationWebhook saves a new notification webhook to the database.
func (d *DB) CreateNotificationWebhook(wh NotificationWebhook) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketNotificationWebhooks)
		data, err := json.Marshal(wh)
		if err != nil {
			return err
		}
		return b.Put([]byte(wh.ID), data)
	})
}

// ListNotificationWebhooks returns all notification webhooks in the database.
func (d *DB) ListNotificationWebhooks() ([]NotificationWebhook, error) {
	var webhooks []NotificationWebhook
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketNotificationWebhooks)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var wh NotificationWebhook
			if err := json.Unmarshal(v, &wh); err != nil {
				return err
			}
			webhooks = append(webhooks, wh)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return webhooks, nil
}

// DeleteNotificationWebhook deletes a notification webhook by its ID.
func (d *DB) DeleteNotificationWebhook(id string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketNotificationWebhooks)
		return b.Delete([]byte(id))
	})
}
