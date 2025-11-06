package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB
var AIdb *sql.DB
var dbMutex sync.Mutex
var my_token = ""

func activateEventForAllUsers(evt string, camid string) {
	// any user in event_table will get this
	sql := fmt.Sprintf(`UPDATE event_table SET active='1'  
	WHERE evt='%v' AND camid='%v'`, evt, camid)
	db.Exec(sql)
	//fmt.Printf("sql: %v", sql)
}

func addCameraRecord(ip string) error {
	sql := "INSERT INTO cameras (ip,sdate) VALUES (?,?)"
	_, err := db.Exec(sql, ip, currentTime())
	return err
}
func add_c3_history(entry string) {
	sql := fmt.Sprintf(`INSERT INTO c3_history (entry) VALUES("%v")`, entry)
	db.Exec(sql)
	//"camid"	INTEGER,
	//				"entry"	TEXT,
}

/*
	func addNotification(from string, to string, msg string) {
		_, err := db.Exec(`INSERT INTO notifications (sender,recipient,txt) VALUES (?,?,?)`, from, to, msg)
		if err != nil {
			fmt.Println("Insert error:", err)
			return
		}
	}
*/
func addNewUserToken(devToken string, userToken string,
	dev string, uztype string, uzphone string) {
	sql := fmt.Sprintf("SELECT dToken,uToken FROM userTokens WHERE dToken='%v' and uToken='%v'",
		devToken, userToken)
	rows, _ := db.Query(sql)

	rows.Next()
	dtk := ""
	utk := ""
	rows.Scan(&dtk, &utk)
	rows.Close()

	if devToken != dtk {
		sql = fmt.Sprintf("INSERT INTO userTokens (dToken,uToken,dev, uztype, uzphone) VALUES ('%v','%v','%v','%v','%v')",
			devToken, userToken, dev, uztype, uzphone)
		_, err := db.Exec(sql)
		if err != nil {

		}
	}
}
func addUserEventForCamera(user string, evt string, camid string) {
	if !userEventExistForCamera(user, evt, camid) { // only add if not exists
		sql := fmt.Sprintf(`INSERT INTO event_table (uzid,evt,camid,active) 
		VALUES ('%v','%v','%v','%v')`,
			user, evt, camid, 0)
		_, err := db.Exec(sql)
		if err == nil {
		}
	}
}
func deleteUserEventForCamera(user string, evt string, camid string) {
	sql := fmt.Sprintf(`DELETE FROM event_table WHERE camid='%v' AND uzid='%v' AND evt='%v'`,
		camid, user, evt)
	_, err := db.Exec(sql)
	if err == nil {
	}
}
func getAllAdminTokens() *sql.Rows {
	sql := "SELECT * FROM userTokens WHERE uztype='mgr'"
	rows, _ := db.Query(sql)
	return rows
}
func getAllDevTokens() *sql.Rows {
	sql := "SELECT * FROM userTokens"
	rows, _ := db.Query(sql)
	return rows
}
func getUsersForActiveEvent(evt string) {
	sql := fmt.Sprintf("SELECT uzid FROM event_table WHERE evt='%v' AND active='1'", evt)
	rows, _ := db.Query(sql)
	fmt.Printf("rows: %v", rows)
}
func getCamIDFromIP(cam string) string {
	sql := fmt.Sprintf("SELECT id FROM cameras WHERE ip='%v'", cam)
	rows, _ := db.Query(sql)
	defer rows.Close()
	rows.Next()
	id := ""

	rows.Scan(&id)

	return id
}
func getCamLabelFromIP(cam string) string {
	sql := fmt.Sprintf("SELECT label FROM cameras WHERE ip='%v'", cam)
	rows, _ := db.Query(sql)
	defer rows.Close()
	rows.Next()
	lbl := ""

	rows.Scan(&lbl)

	return lbl
}
func getDevToken(uToken string) string {
	sql := fmt.Sprintf("SELECT dToken FROM userTokens WHERE uToken='%v'", uToken)
	rows, _ := db.Query(sql)
	defer rows.Close()
	rows.Next()
	dtk := ""

	rows.Scan(&dtk)

	return dtk
}
func change_bldg_access_codes(btoken string, bname string) {
	sql := fmt.Sprintf("UPDATE identity SET bldg_name='%v', bldg_token='%v' WHERE id='1'", bname, btoken)
	_, err := db.Exec(sql)
	if err != nil {
		fmt.Printf("\r\nbldg acc cde error: %v", err.Error())
	}
}
func changeCamSettings(id string, key string, val string) {

	switch key {
	case "0":
		key = "avail"
	case "1":
		key = "allow_pr"
	case "2":
		key = "show_count"
	case "3":
		key = "allow_report"
	case "4":
		key = "ltln"
	case "x":
		key = "label"
	}
	sql := fmt.Sprintf(`UPDATE cameras SET %s='%s' WHERE id='%v'`, key, val, id)
	_, err := db.Exec(sql)
	if err != nil {
		print("\r\nserious db error", err.Error())
	}
	//print("\r\n", sql)
}
func change_cs_ltln(ltln string) {
	sql := fmt.Sprintf("UPDATE identity SET latlng='%v' WHERE id='1'", ltln)
	_, err := db.Exec(sql)
	if err != nil {

	}
}
func getCameraCount() int {
	cnt := 0
	sql := "SELECT count(*) FROM cameras"
	rows, _ := db.Query(sql)
	defer rows.Close()
	rows.Next()
	rows.Scan((&cnt))
	return cnt
}
func getCamLabel(id string) string {
	rtn := ""
	sql := fmt.Sprintf(`SELECT label FROM cameras WHERE id="%s"`, id)
	rows, err := db.Query(sql)
	if err != nil {

	}
	defer rows.Close()
	if err == nil {
		rows.Next()
		rows.Scan(&rtn)
	}
	return rtn
}
func getCameraIPById(id string) (string, error) {
	sql := fmt.Sprintf(`SELECT ip FROM cameras WHERE id="%s"`, id)
	ip := ""
	rows, err := db.Query(sql)
	if err != nil {

	}
	defer rows.Close()
	if err == nil {
		rows.Next()

		rows.Scan(&ip)
		if ip == "" {
			return ip, fmt.Errorf("no cam %s", id)
		}
		return ip, nil
	}
	return ip, err
}

func getCameras() *sql.Rows {
	//sql := fmt.Sprintf() ---- id,ip,label,sdate,avail,allow_pr,show_count,allow_report
	rows, err := db.Query(`SELECT id, ip, [label], [sdate], avail, allow_pr,show_count,allow_report,ltln FROM cameras`)
	if err == nil {
		return rows
	}
	return nil
}
func getCameraRecord(ip string) (*sql.Rows, error) {
	sql := fmt.Sprintf(`SELECT ip,sdate FROM cameras WHERE ip="%s"`, ip)
	rows, err := db.Query(sql)

	if err != nil {
		return nil, err
	}
	return rows, nil
	//defer rows.Close()
}

func get_cs_access_codes() (string, string) {
	bn := ""
	bt := ""
	rows, err := db.Query(`SELECT bldg_name,bldg_token FROM identity`)
	if err != nil {
		return "", ""
	}
	defer rows.Close()
	rows.Next()
	rows.Scan(&bn, &bt)
	return bn, bt
}

func getCS_latlng() string {
	ltln := ""
	rows, err := db.Query(`SELECT latlng FROM identity`)
	if err != nil {
		return "none"
	}
	defer rows.Close()
	rows.Next()
	rows.Scan((&ltln))

	return ltln
}

func getPhoneFromToken(tok string) string {
	phone := ""
	sql := fmt.Sprintf("SELECT uzphone from userTokens WHERE uToken='%v'", tok)
	rows, err := db.Query(sql)
	if err == nil {
		defer rows.Close()
		rows.Next()
		rows.Scan(&phone)
	}
	return phone
}

func getUUID() string {
	uid := "0"
	if my_token != "" {
		return my_token
	}
	sql := "SELECT uuid FROM identity WHERE id='1'"
	rows, err := db.Query(sql)
	if err != nil {
		return ""
	}
	defer rows.Close()
	rows.Next()
	rows.Scan(&uid)

	if uid == "0" {
		uid = genUUID()
		saveUUID(uid)
	}
	print("\r\nuuid string is: ", uid)

	my_token = uid
	return uid
}

/*
	func getNotificationsFor(userToken string) {
		sql := fmt.Sprintf(`SELECT txt FROM notifications WHERE recipient="%s"`, userToken)
		rows, err := db.Query(sql)
		if err != nil {
			fmt.Println("Query error:", err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var rec string
			rows.Scan(&rec)
			fmt.Println("\r\nMessage:", rec)
		}
	}
*/
func initCamDetectionDB(cam string) {
	cam = strings.ReplaceAll(cam, ".", "_")
	sql := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS AI%v (id INTEGER PRIMARY KEY,
				   obj TEXT, amplitude TEXT, x TEXT, w TEXT, ts DATETIME DEFAULT (DATETIME('now','localtime')))`, cam)

	dbMutex.Lock()
	defer dbMutex.Unlock()
	_, err := AIdb.Exec(sql)
	if err != nil {
		fmt.Printf("\r\n%v table creation error -> %v", cam, err.Error())
	}
}

func initDatabase(szdb string) *sql.DB {
	var err error
	db, err = sql.Open("sqlite3", szdb)
	AIdb, _ = sql.Open("sqlite3", szdb)
	if err != nil {
		fmt.Println("Error:", err)
		return nil
	}
	sql := ""
	switch szdb {
	case "./camview.db":
		sql = `CREATE TABLE IF NOT EXISTS notifications (id INTEGER PRIMARY KEY, 
		sender TEXT,recipient TEXT, sdate TEXT, txt TEXT)`
		_, err = db.Exec(sql)
		if err == nil {
			sql = `CREATE TABLE IF NOT EXISTS cameras (id INTEGER PRIMARY KEY, 
		           ip TEXT,label TEXT, sdate TEXT,avail INTEGER DEFAULT (0), 
				   allow_pr INTEGER  DEFAULT (0),show_count INTEGER  DEFAULT (0),
				   allow_report INTEGER  DEFAULT (0),ltln TEXT)`
			_, err = db.Exec(sql)
		}
		if err == nil {
			sql = `CREATE TABLE IF NOT EXISTS identity (id INTEGER PRIMARY KEY, 
		           uuid TEXT, latlng TEXT, bldg_name TEXT, bldg_token TEXT, sdate TEXT)`
			_, err = db.Exec(sql)
		}
		if err == nil {
			sql = `CREATE TABLE IF NOT EXISTS userTokens (id INTEGER PRIMARY KEY,
				   dToken TEXT, uToken TEXT, dev TEXT, uztype TEXT, uzphone TEXT, 
				   ts DATETIME DEFAULT (DATETIME('now','localtime')))`
			_, err = db.Exec(sql)
		}
		if err == nil {
			sql = `CREATE TABLE IF NOT EXISTS event_table (
					"id"	INTEGER,
					"camid"	INTEGER,
					"uzid"	TEXT,
					"evt"	TEXT,
					"active" INTEGER,
					PRIMARY KEY("id")
			)`
			_, err = db.Exec(sql)
		}
		if err == nil {
			sql = `CREATE TABLE IF NOT EXISTS c3_history (
					"id"	INTEGER,
					"entry"	TEXT,
					ts DATETIME DEFAULT (DATETIME('now','localtime')),
					PRIMARY KEY("id")
			)`
			_, err = db.Exec(sql)
		}
	default:
		db = nil
	}

	if err != nil {
		fmt.Println("Create table error:", err)
		return nil
	}
	return db
}
func isEventActive(evt string, user string, camid string) bool {
	yes := false
	uzid := ""
	sql := fmt.Sprintf(`SELECT uzid FROM event_table WHERE evt='%v' 
	AND uzid='%v' AND camid='%v' AND active='1'`,
		evt, user, camid)
	rows, err := db.Query(sql)
	if err == nil {
		defer rows.Close()
		rows.Next()
		rows.Scan(&uzid)
		if uzid == user {
			yes = true
		}
	}
	return yes
}

func rowsToStruct(rows *sql.Rows, dest interface{}) error {
	defer rows.Close()

	columns, _ := rows.Columns()
	var result []map[string]interface{}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		rows.Scan(pointers...)
		rowMap := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				rowMap[col] = string(b)
			} else {
				rowMap[col] = val
			}
		}
		result = append(result, rowMap)
	}
	data, _ := json.Marshal(result)
	return json.Unmarshal(data, dest)
}

func saveUUID(u string) {
	sql := fmt.Sprintf(`INSERT INTO identity (uuid) VALUES('%v')`, u)
	_, err := db.Exec(sql)
	if err != nil {
		print("\r\nserious db error", err.Error())
	}
}

func userEventExistForCamera(user string, evt string, camid string) bool {
	yes := false
	uzid := ""
	sql := fmt.Sprintf(`SELECT uzid FROM event_table WHERE evt='%v' 
	AND uzid='%v' AND camid='%v'`,
		evt, user, camid)
	rows, err := db.Query(sql)
	if err == nil {
		defer rows.Close()
		rows.Next()
		rows.Scan(&uzid)
		if uzid == user {
			yes = true
		}
	}

	return yes
}
