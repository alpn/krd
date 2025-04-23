package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed template.html
var htmlTemplate string

type Row struct {
	//	Rowid int
	Vals []interface{}
}

type PageData struct {
	Name string
	Cols []string
	Rows []Row
}

type Table struct {
	Name   string
	Schema string
}

type TablesPageData struct {
	Tables []Table
	Name   string
}

func isBlob(db *sql.DB, table string, column string) bool {

	iquery := fmt.Sprintf("PRAGMA table_info(%s)", table)
	rows, err := db.Query(iquery)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {

		var _cid sql.NullString
		var _name sql.NullString
		var _type sql.NullString
		var _notnull sql.NullString
		var _dflt_value sql.NullString
		var _pk sql.NullString

		err = rows.Scan(&_cid, &_name, &_type, &_notnull, &_dflt_value, &_pk)
		if err != nil {
			log.Fatal(err)
		}

		if _name.Valid && _name.String == column {
			if _type.String == "BLOB" {
				return true
			}
		}
	}

	return false
}

func handleRemove(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		vars := mux.Vars(r)
		sqlDeleteRow := fmt.Sprintf(`DELETE FROM %s WHERE ROWID=%s`, vars["table"], vars["rowid"])

		res, err := db.Exec(sqlDeleteRow)
		if err != nil {
			log.Printf("db: %v", err)
		} else {
			ra, err := res.RowsAffected()
			log.Printf("db (ok): %v %v", ra, err)
		}
		rd := fmt.Sprintf("/t/%s", vars["table"])
		http.Redirect(w, r, rd, http.StatusSeeOther)
	}
}

func handleDuplicate(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		if "GET" == r.Method {
			duplicateStatement := `insert into %s select * from %s where rowid=%s`

			whatever := strings.TrimPrefix(r.URL.Path, "/dup/")
			args := strings.Split(whatever, "/")

			ss := fmt.Sprintf(duplicateStatement, args[0], args[0], args[1])

			_, err := db.Exec(ss)
			if err != nil {
				w.Write([]byte(fmt.Sprintf("%v", err)))
			} else {
				rd := fmt.Sprintf("/t/%s", args[0])
				http.Redirect(w, r, rd, http.StatusSeeOther)
			}
		}
	}
}

func handleInsertUpdate(db *sql.DB, w http.ResponseWriter, r *http.Request, op string) {

	if "POST" != r.Method {
		return
	}

	if err := r.ParseForm(); err != nil {
		fmt.Fprintf(w, "ParseForm() err: %+v", err)
		return
	}

	formValues := r.Form

	var sb strings.Builder

	table := r.FormValue("table")
	sb.WriteString(op)
	sb.WriteString(` into `)
	sb.WriteString(table)

	var ksb strings.Builder
	var vsb strings.Builder

	ksb.WriteString(" (")
	vsb.WriteString(` values(`)

	for k, v := range formValues {

		if k == "table" || k == "rowid" {
			continue
		}

		ksb.WriteString(`"`)
		ksb.WriteString(k)
		ksb.WriteString(`",`)

		vsb.WriteString(`"`)
		vsb.WriteString(v[0])
		vsb.WriteString(`",`)
	}

	kstr := ksb.String()
	vstr := vsb.String()

	kstr2 := kstr[:len(kstr)-1] + string(")")
	vstr2 := vstr[:len(vstr)-1] + string(")")

	sb.WriteString(kstr2)
	sb.WriteString(vstr2)
	finalStr := sb.String()

	_, err := db.Exec(finalStr)
	if err != nil {
		w.Write([]byte(fmt.Sprintf("Error: %+v", err)))
	} else {
		http.Redirect(w, r, fmt.Sprintf("/t/%s", table), http.StatusSeeOther)
	}
}

func handleAdd(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		handleInsertUpdate(db, w, r, "insert")
	}
}

func handleUpdate(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		if "POST" != r.Method {
			return
		}

		if err := r.ParseForm(); err != nil {
			fmt.Fprintf(w, "ParseForm() err: %+v", err)
			return
		}

		formValues := r.Form
		var sb strings.Builder

		table := r.FormValue("table")
		sb.WriteString("update ")
		sb.WriteString(table)
		sb.WriteString(` set `)

		var usb strings.Builder

		for k, v := range formValues {

			if k == "table" || k == "rowid" {
				continue
			}

			if isBlob(db, table, k) {
				continue
			}

			usb.WriteString(k)

			usb.WriteString(`=`)
			usb.WriteString(`"`)
			usb.WriteString(v[0])
			usb.WriteString(`"`)
			usb.WriteString(`,`)
		}

		str := usb.String()
		str2 := str[:len(str)-1]

		sb.WriteString(str2)
		sb.WriteString("where rowid=")
		if nil != formValues["rowid"] {
			if 0 < len(formValues["rowid"]) {
				sb.WriteString(formValues["rowid"][0])
			}
		}
		finalStr := sb.String()

		fmt.Println("update query: " + finalStr)
		_, err := db.Exec(finalStr)
		if err != nil {
			w.Write([]byte(fmt.Sprintf("SQL Error:\n")))
			w.Write([]byte(fmt.Sprintf("query : %s\n", finalStr)))
			w.Write([]byte(fmt.Sprintf("error: %+v\n", err)))
		} else {
			http.Redirect(w, r, fmt.Sprintf("/t/%s", table), http.StatusSeeOther)
		}
	}

}

func showTable(db *sql.DB, w http.ResponseWriter, r *http.Request, name string, query string) {

	w.Header().Add("Content-Type", "text/html ; charset=utf-8")

	rows, err := db.Query(query)
	if err != nil {
		w.Write([]byte(fmt.Sprintf("%+v", err)))
		return
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		w.Write([]byte(fmt.Sprintf("%+v", err)))
		return
	}

	var data PageData
	data.Name = name
	data.Cols = cols

	for rows.Next() {

		columns := make([]interface{}, len(cols))
		for i := range columns {
			columns[i] = new(interface{})
		}

		if err := rows.Scan(columns...); err != nil {
			w.Write([]byte(fmt.Sprintf("%+v", err)))
			return
		}

		var r Row
		r.Vals = columns
		data.Rows = append(data.Rows, r)
	}

	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("%+v", cols)
	tmpl, _ := template.New("").Parse(htmlTemplate)
	//    tmpl, _ := template.ParseFiles("./template.html")
	tmpl.Execute(w, data)

}

func handleSort(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		path := strings.TrimPrefix(r.URL.Path, "/s/")
		args := strings.Split(path, "/")

		name := args[0]
		sortBy := args[1]

		query := fmt.Sprintf("select rowid,* from %s order by %s", name, sortBy)
		showTable(db, w, r, name, query)
	}
}

func handleShowTable(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/t/")

		iquery := fmt.Sprintf("PRAGMA table_info(%s)", name)
		rows, err := db.Query(iquery)
		if err != nil {
			w.Write([]byte(fmt.Sprintf("%+v", err)))
			return
		}

		var sb strings.Builder
		sb.WriteString("SELECT rowid AS rowid")
		index := 0

		for rows.Next() {

			var _cid sql.NullString
			var _name sql.NullString
			var _type sql.NullString
			var _notnull sql.NullString
			var _dflt_value sql.NullString
			var _pk sql.NullString

			err = rows.Scan(&_cid, &_name, &_type, &_notnull, &_dflt_value, &_pk)
			if err != nil {
				log.Fatal(err)
			}

			if _pk.String == "1" {
				fmt.Println(_name.String + " is prime key")
			}
			if _name.Valid && _type.Valid {
				if _type.String == "BLOB" {
					sb.WriteString(",'BLOB' AS " + _name.String)

				} else {
					sb.WriteString("," + _name.String)
				}
			}
			index++
		}

		defer rows.Close()

		sb.WriteString(" FROM " + name)
		query := sb.String()

		fmt.Println(query)
		showTable(db, w, r, name, query)
	}
}

// TODO: add util function to get type from name and adjust query accordingly, instead of this (handleSort too)
func handleView(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/v/")
		query := fmt.Sprintf("select * from %s", name)
		showTable(db, w, r, name, query)
	}
}

func handleShowAll(db *sql.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		w.Header().Add("Content-Type", "text/html ; charset=utf-8")

		rows, err := db.Query("select * from sqlite_schema")
		if err != nil {
			w.Write([]byte(fmt.Sprintf("%+v", err)))
			return
		}

		defer rows.Close()

		for rows.Next() {

			var _type string
			var _name string
			var _tbl_name string
			var _rootpage string
			var _sql string

			err = rows.Scan(&_type, &_name, &_tbl_name, &_rootpage, &_sql)
			if err != nil {
				if _type == "index" {
					log.Print("Bad index (skipping)")
					log.Print(err)
					continue
				}
				log.Fatal(err)
			}

			if _type == "table" {
				w.Write([]byte(fmt.Sprintf("<div><a href='/t/%s'>%s</a></div>", _name, _name)))
			}

			if _type == "view" {
				w.Write([]byte(fmt.Sprintf("<div><a href='/v/%s'>%s</a></div>", _name, _name)))
			}

		}

		err = rows.Err()
		if err != nil {
			log.Fatal(err)
		}

	}
}

func makeRouter(db *sql.DB) *mux.Router {

	router := mux.NewRouter()
	router.HandleFunc("/", handleShowAll(db))
	router.HandleFunc("/t/{table}", handleShowTable(db))
	router.HandleFunc("/a", handleAdd(db))
	router.HandleFunc("/d/{table}/{rowid}", handleRemove(db)).Methods(http.MethodGet)
	router.HandleFunc("/dup/", handleDuplicate(db))
	router.HandleFunc("/u", handleUpdate(db))
	router.HandleFunc("/s/", handleSort(db))
	router.HandleFunc("/v/", handleView(db))

	return router
}

func main() {

	args := os.Args[1:]
	if len(args) != 1 {
		fmt.Println("Usage: krd sqlite.db")
		return
	}

	db, err := sql.Open("sqlite3", args[0])
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		log.Println("closing db")
		db.Close()
	}()

	portNumberStr := strconv.Itoa(9797)
	addr := "localhost:" + portNumberStr
	log.Println("http://" + addr)

	router := makeRouter(db)
	server := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil {
			fmt.Println(err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		log.Println("\nreceived signal, shutting down...")
		cancel()
	}()

	<-ctx.Done()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("err %v", err)
	}

	log.Println("bye")

}
