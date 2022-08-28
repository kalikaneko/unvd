package main

import (
	"log"

	unvd "github.com/kalikaneko/unvd"
)

func main() {
	client, err := unvd.NewClient("tmp")
	if err != nil {
		log.Fatal(err)
	}
	err = client.PrefetchYear("2022")
	if err != nil {
		panic(err)
	}
	desc, err := client.GetDescription("CVE-2022-0615")
	if err != nil {
		panic(err)
	}
	log.Println("==>", desc)
	expected := `Use-after-free in eset_rtp kernel module used in ESET products for Linux allows potential attacker to trigger denial-of-service condition on the system.`
	if desc == expected {
		log.Println("OK")
	} else {
		log.Fatal("wrong description")
	}

}
