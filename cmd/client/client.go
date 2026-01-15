package main

import (
	"log"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	u := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/ws"}
	
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	config := map[string]interface{}{
		"mode": "fft",
		"channels": []string{"I0", "Q0"},
		"fps": 10,
	}
	c.WriteJSON(config)

	for i := 0; i < 50; i++ {
		_, _, err := c.ReadMessage()
		if err != nil {
			return
		}
	}
    time.Sleep(3 * time.Second)
}
