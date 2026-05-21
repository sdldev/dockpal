package db

import (
	"encoding/json"

	"go.etcd.io/bbolt"
)

type Webhook struct {
	ID          string `json:"id"`
	InstanceID  string `json:"instance_id"`
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch,omitempty"`
	ComposeFile string `json:"compose_file,omitempty"`
	Secret      string `json:"secret,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// CreateWebhook saves a new webhook to the database.
func (d *DB) CreateWebhook(wh Webhook) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		data, err := json.Marshal(wh)
		if err != nil {
			return err
		}
		return b.Put([]byte(wh.ID), data)
	})
}

// GetWebhook retrieves a webhook by its ID.
func (d *DB) GetWebhook(id string) (*Webhook, error) {
	var wh Webhook
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		data := b.Get([]byte(id))
		if data == nil {
			return ErrWebhookNotFound
		}
		return json.Unmarshal(data, &wh)
	})
	if err != nil {
		return nil, err
	}
	return &wh, nil
}

// ListWebhooks returns all webhooks in the database.
func (d *DB) ListWebhooks() ([]Webhook, error) {
	var webhooks []Webhook
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		return b.ForEach(func(k, v []byte) error {
			var wh Webhook
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

// DeleteWebhook deletes a webhook by its ID.
func (d *DB) DeleteWebhook(id string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		return b.Delete([]byte(id))
	})
}
