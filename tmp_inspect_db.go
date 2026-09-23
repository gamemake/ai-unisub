package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"fmt"
	"log"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "data/ai-unisub.db")
	if err != nil { log.Fatal(err) }
	defer db.Close()
	for _, table := range []string{"call_traces_20260920", "call_traces_20260921", "call_traces_20260922", "call_traces_20260923"} {
		rows, err := db.Query("SELECT request_id, id, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, response_headers, response_body FROM \""+table+"\" WHERE request_id = ?", "d93fba5f740f437b22edf94e83e6f2d6")
		if err != nil { log.Fatal(err) }
		for rows.Next() {
			var id, input, output, cc, cr int
			var requestID, model string
			var body []byte
			var headers string
			if err := rows.Scan(&requestID, &id, &model, &input, &output, &cc, &cr, &headers, &body); err != nil { log.Fatal(err) }
			fmt.Printf("table=%s id=%d model=%q input=%d output=%d cache_creation=%d cache_read=%d headers=%s\\n", table, id, model, input, output, cc, cr, headers)
			if r, err := gzip.NewReader(bytes.NewReader(body)); err == nil { var decoded bytes.Buffer; _, _ = decoded.ReadFrom(r); _ = r.Close(); body = decoded.Bytes() }
			fmt.Printf("body=%s\\n", body)
		}
		rows.Close()
	}
}
