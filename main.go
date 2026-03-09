package main

func main() {

	loadenv()
	db, key := opendb()
	defer db.Close()
	runBot(db, key)

}
