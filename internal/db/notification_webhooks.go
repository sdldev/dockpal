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

