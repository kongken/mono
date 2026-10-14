// Command vapid-keygen prints a new VAPID key pair for the notification.web_push
// settings (F12). Keep the private key secret; the public key is sent to browsers.
package main

import (
	"fmt"
	"log"

	"github.com/SherClockHolmes/webpush-go"
)

func main() {
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("notification:\n  web_push:\n    vapid_public_key: %s\n    vapid_private_key: %s\n    subject: mailto:you@example.com\n", public, private)
}
