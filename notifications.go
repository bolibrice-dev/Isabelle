package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Note struct {
	Sender    string `json:"sender"`
	Recipient string `json:"recipient"`
	Text      string `json:"text"`
	SendDate  string `json:"send_date"`
}

type NotesWrapper struct {
	Notes map[string]Note `json:"notes"`
}

var theNotes = loadNotes()

func loadNotes() *Note {
	var tmp Note
	var data NotesWrapper
	dt, err := os.ReadFile("notifications.json")

	if err == nil {
		err = json.Unmarshal(dt, &data)
		if err == nil {
			for i, v := range data.Notes {
				fmt.Printf("\r\n%s %v", i, v.Recipient)
				//fmt.Printf("\r\n%v %v\r\n", i, v)
			}
		}
	}

	return &tmp
}
