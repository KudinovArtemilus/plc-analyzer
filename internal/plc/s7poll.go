package plc

import (
	"time"

	"github.com/robinson/gos7"
)

// Client — подключение к одному ПЛК
type Client struct {
	handler *gos7.TCPClientHandler
	client  gos7.Client
}

// Connect открывает соединение S7comm
func Connect(ip string, rack, slot int) (*Client, error) {
	h := gos7.NewTCPClientHandler(ip, rack, slot)
	h.Timeout = 2 * time.Second
	if err := h.Connect(); err != nil {
		return nil, err
	}
	return &Client{handler: h, client: gos7.NewClient(h)}, nil
}

// Close закрывает соединение
func (c *Client) Close() {
	c.handler.Close()
}

// Read читает весь блок одним запросом в buf
func (c *Client) Read(b Block, buf []byte) error {
	return c.client.AGReadDB(b.DB, b.Start, b.Size, buf)
}
