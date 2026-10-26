package server

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// seedDemo gives the owner something to explore (DEMO_DATA=true, for
// screenshots and trying the app): a small shop as a real SQLite file with
// a year of orders, a notes app's file, some servers that only exist on
// paper, saved queries, history, changes and three months of growth.
// Runs once.
func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	key := fmt.Sprintf("seeded:%d:demo", owner)
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	dir := s.uploadDir(owner)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	shop := filepath.Join(dir, "demo-shop.db")
	notes := filepath.Join(dir, "demo-notes.db")
	for _, p := range []string{shop, notes} {
		_ = os.Remove(p)
	}
	shopSize, err := buildShop(ctx, shop, rng)
	if err != nil {
		return fmt.Errorf("demo shop: %w", err)
	}
	notesSize, err := buildNotes(ctx, notes, rng)
	if err != nil {
		return fmt.Errorf("demo notes: %w", err)
	}
	now := time.Now().UTC()
	checked := now.Add(-3 * time.Minute).Format(time.RFC3339Nano)
	seal := func(p string) string { v, _ := s.box.Seal(p); return v }
	conns := []*store.Connection{
		{Name: "Shop", Engine: "sqlite", Path: shop, Group: "Demo", Source: "demo", AllowWrites: true, Monitor: true, Favorite: true, Status: "up", Version: "3.50", SizeBytes: shopSize, LatencyMs: 2, CheckedAt: checked, Color: "#3d5a99",
			Notes: "A small online shop: customers, products, orders. Try the SQL editor or Ask AI."},
		{Name: "Notes app", Engine: "sqlite", Path: notes, Group: "Demo", Source: "demo", Monitor: true, Status: "up", Version: "3.50", SizeBytes: notesSize, LatencyMs: 1, CheckedAt: checked},
		{Name: "photos", Engine: "postgres", Host: "photos-db.demo.invalid", Port: 5432, Username: "photos", Password: seal("demo"), Database: "photos", Group: "photos", Source: "demo",
			Monitor: true, Status: "up", Version: "16.4", SizeBytes: 3_412_000_000, LatencyMs: 4, CheckedAt: checked},
		{Name: "cloud", Engine: "postgres", Host: "cloud-db.demo.invalid", Port: 5432, Username: "cloud", Password: seal("demo"), Database: "cloud", Group: "cloud", Source: "demo",
			Monitor: true, Status: "up", Version: "17.2", SizeBytes: 912_000_000, LatencyMs: 3, CheckedAt: checked},
		{Name: "finance", Engine: "mysql", Host: "finance-db.demo.invalid", Port: 3306, Username: "finance", Password: seal("demo"), Database: "finance", Group: "finance", Source: "demo",
			Monitor: true, Status: "up", Version: "MariaDB 11.4.3", SizeBytes: 84_000_000, LatencyMs: 2, CheckedAt: checked},
		{Name: "cache", Engine: "redis", Host: "cache.demo.invalid", Port: 6379, Database: "0", Group: "cloud", Source: "demo",
			Monitor: true, Status: "up", Version: "Valkey 8.1.1", SizeBytes: 21_500_000, LatencyMs: 1, CheckedAt: checked},
		{Name: "wiki", Engine: "postgres", Host: "wiki-db.demo.invalid", Port: 5432, Username: "wiki", Password: seal("demo"), Database: "wiki", Group: "wiki", Source: "demo",
			Monitor: true, Status: "down", StatusDetail: "cannot connect: the container wiki_db is stopped", Version: "15.8", SizeBytes: 156_000_000, CheckedAt: checked,
			DownSince: now.Add(-47 * time.Minute).Format(time.RFC3339Nano)},
	}
	ids := map[string]int64{}
	for _, c := range conns {
		if err := s.store.CreateConnection(ctx, owner, c); err != nil {
			return err
		}
		_ = s.store.SetConnectionStatus(ctx, c)
		ids[c.Name] = c.ID
	}
	// three months of growth
	today, _ := store.ParseDate(store.Today())
	for _, c := range conns {
		size := float64(c.SizeBytes)
		rate := 1.0
		if c.Name == "photos" {
			rate = 3
		}
		for d := 0; d < 90; d++ {
			_ = s.store.AddSize(ctx, owner, c.ID, today.AddDate(0, 0, -d).Format("2006-01-02"), int64(size))
			size /= 1 + (0.0015+rng.Float64()*0.004)*rate
		}
	}
	// saved queries
	shopID := ids["Shop"]
	saved := []store.SavedQuery{
		{ConnectionID: &shopID, Name: "Revenue by month", Favorite: true, Description: "Orders that were not cancelled, last twelve months.",
			Body: "SELECT strftime('%Y-%m', created_at) AS month,\n       COUNT(*) AS orders,\n       ROUND(SUM(total), 2) AS revenue\nFROM orders\nWHERE status <> 'cancelled'\nGROUP BY month\nORDER BY month DESC\nLIMIT 12;"},
		{ConnectionID: &shopID, Name: "Best-selling products", Description: "Units and revenue per product.",
			Body: "SELECT p.name, p.category,\n       SUM(i.quantity) AS units,\n       ROUND(SUM(i.quantity * i.unit_price), 2) AS revenue\nFROM order_items i\nJOIN products p ON p.id = i.product_id\nGROUP BY p.id\nORDER BY units DESC\nLIMIT 20;"},
		{ConnectionID: &shopID, Name: "Customers who never ordered",
			Body: "SELECT c.name, c.email, c.city, c.created_at\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nWHERE o.id IS NULL\nORDER BY c.created_at;"},
		{ConnectionID: &shopID, Name: "Low stock", Description: "Products to reorder.",
			Body: "SELECT name, category, stock, price\nFROM products\nWHERE stock < 10 AND active = 1\nORDER BY stock;"},
		{Name: "Biggest tables (PostgreSQL)", Description: "Works on any PostgreSQL connection.",
			Body: "SELECT relname AS table,\n       pg_size_pretty(pg_total_relation_size(oid)) AS size,\n       reltuples::bigint AS rows\nFROM pg_class\nWHERE relkind = 'r'\nORDER BY pg_total_relation_size(oid) DESC\nLIMIT 15;"},
	}
	for i := range saved {
		_ = s.store.CreateSaved(ctx, owner, &saved[i])
		if i < 3 {
			for n := 0; n < 3+i*2; n++ {
				s.store.RanSaved(ctx, owner, saved[i].ID)
			}
		}
	}
	// history
	hist := []struct {
		conn, body string
		rows, ms   int64
		err        string
		ago        time.Duration
	}{
		{"Shop", "UPDATE products SET stock = stock + 25 WHERE id = 17;", 1, 5, "", 50 * time.Hour},
		{"Notes app", "SELECT title, updated_at FROM notes ORDER BY updated_at DESC LIMIT 20;", 20, 2, "", 30 * time.Hour},
		{"Shop", saved[1].Body, 20, 22, "", 26 * time.Hour},
		{"Shop", "SELECT * FROM order WHERE id = 10;", 0, 1, `near "order": syntax error`, 3 * time.Hour},
		{"Shop", "SELECT status, COUNT(*) FROM orders GROUP BY status;", 4, 3, "", 2 * time.Hour},
		{"Shop", "SELECT * FROM customers WHERE city = 'Sarajevo' ORDER BY name;", 31, 4, "", 25 * time.Minute},
		{"Shop", saved[0].Body, 12, 18, "", 6 * time.Minute},
	}
	for _, h := range hist {
		_ = s.store.AddHistory(ctx, owner, &store.HistoryEntry{ConnectionID: ids[h.conn], Body: h.body, OK: h.err == "", Error: h.err, Rows: h.rows, DurationMs: h.ms,
			CreatedAt: now.Add(-h.ago).Format(time.RFC3339Nano)})
	}
	// changes
	acts := []struct {
		conn, kind, target, summary, stmt string
		rows                              int64
		ago                               time.Duration
	}{
		{"Shop", "insert", "customers", "added 1 row", `INSERT INTO "customers" ("city", "email", "name") VALUES (?, ?, ?)`, 1, 120 * time.Hour},
		{"cache", "key", "session:8f2c", "Deleted session:8f2c", "", 0, 75 * time.Hour},
		{"Shop", "sql", "", "Ran UPDATE (1 row)", "UPDATE products SET stock = stock + 25 WHERE id = 17;", 1, 50 * time.Hour},
		{"Shop", "snapshot", "demo-shop.db", "Before a change", "", 0, 41 * time.Minute},
		{"Shop", "edit", "products", "changed 1 row", `UPDATE "products" SET "price" = ? WHERE "id" = ?`, 1, 40 * time.Minute},
	}
	for _, a := range acts {
		id := ids[a.conn]
		_ = s.store.AddActivity(ctx, owner, &store.Activity{ConnectionID: &id, ConnectionName: a.conn, Kind: a.kind, Target: a.target, Summary: a.summary,
			Statement: a.stmt, Rows: a.rows, OK: true, CreatedAt: now.Add(-a.ago).Format(time.RFC3339Nano)})
	}
	return s.store.CachePut(ctx, key, "1")
}

func openDemo(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(DELETE)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func buildShop(ctx context.Context, path string, rng *rand.Rand) (int64, error) {
	db, err := openDemo(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	schema := `
CREATE TABLE customers (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  email      TEXT NOT NULL UNIQUE,
  city       TEXT NOT NULL DEFAULT '',
  country    TEXT NOT NULL DEFAULT 'BA',
  newsletter INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE products (
  id       INTEGER PRIMARY KEY,
  sku      TEXT NOT NULL UNIQUE,
  name     TEXT NOT NULL,
  category TEXT NOT NULL,
  price    REAL NOT NULL,
  stock    INTEGER NOT NULL DEFAULT 0,
  active   INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE orders (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  status      TEXT NOT NULL DEFAULT 'paid' CHECK (status IN ('paid', 'shipped', 'delivered', 'cancelled')),
  total       REAL NOT NULL DEFAULT 0,
  note        TEXT,
  created_at  TEXT NOT NULL
);
CREATE INDEX orders_customer ON orders(customer_id);
CREATE INDEX orders_created ON orders(created_at);
CREATE TABLE order_items (
  order_id   INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id INTEGER NOT NULL REFERENCES products(id),
  quantity   INTEGER NOT NULL,
  unit_price REAL NOT NULL,
  PRIMARY KEY (order_id, product_id)
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE VIEW order_summary AS
  SELECT o.id, c.name AS customer, o.status, o.total, o.created_at
  FROM orders o JOIN customers c ON c.id = o.customer_id;
`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	first := []string{"Amra", "Emir", "Lejla", "Dino", "Selma", "Haris", "Maja", "Nermin", "Ivana", "Tarik", "Ana", "Kenan", "Mia", "Adnan", "Sara", "Luka", "Ema", "Faris", "Nina", "Edin", "Lana", "Mirza", "Iva", "Damir", "Una", "Vedad", "Hana", "Ismar", "Zara", "Jasmin"}
	last := []string{"Hodžić", "Kovačević", "Begić", "Delić", "Marić", "Hadžić", "Jukić", "Babić", "Mehić", "Petrović", "Alić", "Novak", "Šarić", "Tomić", "Husić", "Vuković"}
	cities := []string{"Sarajevo", "Sarajevo", "Sarajevo", "Mostar", "Banja Luka", "Tuzla", "Zenica", "Bihać", "Zagreb", "Split", "Belgrade", "Ljubljana", "Vienna"}
	country := map[string]string{"Zagreb": "HR", "Split": "HR", "Belgrade": "RS", "Ljubljana": "SI", "Vienna": "AT"}
	start := time.Now().AddDate(-1, -2, 0)
	nCust := 240
	for i := 1; i <= nCust; i++ {
		fn, ln := first[rng.Intn(len(first))], last[rng.Intn(len(last))]
		email := strings.ToLower(fmt.Sprintf("%s.%s%d@example.com", asciiName(fn), asciiName(ln), i))
		created := start.Add(time.Duration(rng.Int63n(int64(time.Since(start))))).UTC().Format("2006-01-02 15:04:05")
		city := cities[rng.Intn(len(cities))]
		if _, err := tx.ExecContext(ctx, `INSERT INTO customers (id, name, email, city, country, newsletter, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			i, fn+" "+ln, email, city, firstNonEmpty(country[city], "BA"), rng.Intn(3) == 0, created); err != nil {
			return 0, err
		}
	}
	products := []struct {
		name, cat string
		price     float64
	}{
		{"Ceramic coffee mug", "Kitchen", 12.5}, {"Cast iron pan 28 cm", "Kitchen", 54.9}, {"Chef's knife", "Kitchen", 79}, {"Bamboo cutting board", "Kitchen", 24.9},
		{"French press 1 l", "Kitchen", 34.5}, {"Linen tea towels (2)", "Kitchen", 15.9}, {"Wool throw blanket", "Home", 69}, {"Scented candle, fig", "Home", 19.9},
		{"Desk lamp, brass", "Home", 89}, {"Plant pot, terracotta", "Home", 14.5}, {"Wall clock, oak", "Home", 45}, {"Cotton cushion cover", "Home", 22},
		{"Notebook A5, dotted", "Stationery", 9.9}, {"Fountain pen", "Stationery", 39}, {"Desk organiser", "Stationery", 27.5}, {"Watercolour set", "Stationery", 31},
		{"Hiking socks", "Outdoor", 16.9}, {"Thermos 0.75 l", "Outdoor", 29.9}, {"Rain jacket", "Outdoor", 119}, {"Daypack 20 l", "Outdoor", 74.9},
		{"Yoga mat", "Outdoor", 39.9}, {"Bike light set", "Outdoor", 27}, {"Board game: Sarajevo", "Games", 44}, {"Puzzle 1000 pieces", "Games", 21.5},
		{"Card game", "Games", 12}, {"Chess set, walnut", "Games", 95}, {"Olive oil 0.5 l", "Food", 13.9}, {"Wild honey 450 g", "Food", 11.5},
		{"Dried figs 250 g", "Food", 7.9}, {"Coffee beans 1 kg", "Food", 26.9},
	}
	for i, p := range products {
		if _, err := tx.ExecContext(ctx, `INSERT INTO products (id, sku, name, category, price, stock, active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			i+1, fmt.Sprintf("%s-%03d", strings.ToUpper(p.cat[:3]), i+1), p.name, p.cat, p.price, rng.Intn(80), i != 25); err != nil {
			return 0, err
		}
	}
	statuses := []string{"delivered", "delivered", "delivered", "delivered", "shipped", "paid", "cancelled"}
	notes := []string{"", "", "", "", "Gift wrap, please", "Leave at the door", "Call before delivery", ""}
	oid := 0
	for d := start; d.Before(time.Now()); d = d.Add(24 * time.Hour) {
		n := 2 + rng.Intn(6)
		if d.Month() == time.December {
			n += 5
		}
		for k := 0; k < n; k++ {
			oid++
			cust := 1 + rng.Intn(nCust-30) // the newest customers never ordered
			at := d.Add(time.Duration(8+rng.Intn(14))*time.Hour + time.Duration(rng.Intn(60))*time.Minute).UTC()
			status := statuses[rng.Intn(len(statuses))]
			if time.Since(at) < 72*time.Hour {
				status = []string{"paid", "shipped"}[rng.Intn(2)]
			}
			var note any
			if t := notes[rng.Intn(len(notes))]; t != "" {
				note = t
			}
			total := 0.0
			used := map[int]bool{}
			var items [][3]float64
			for j := 0; j < 1+rng.Intn(3); j++ {
				pid := 1 + rng.Intn(len(products))
				if used[pid] {
					continue
				}
				used[pid] = true
				q := 1 + rng.Intn(3)
				price := products[pid-1].price
				total += float64(q) * price
				items = append(items, [3]float64{float64(pid), float64(q), price})
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO orders (id, customer_id, status, total, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				oid, cust, status, float64(int(total*100))/100, note, at.Format("2006-01-02 15:04:05")); err != nil {
				return 0, err
			}
			for _, it := range items {
				if _, err := tx.ExecContext(ctx, `INSERT INTO order_items (order_id, product_id, quantity, unit_price) VALUES (?, ?, ?, ?)`, oid, int(it[0]), int(it[1]), it[2]); err != nil {
					return 0, err
				}
			}
		}
	}
	for _, kv := range [][2]string{{"currency", "EUR"}, {"shipping_free_from", "60"}, {"store_name", "Kuća & Co."}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, kv[0], kv[1]); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	_, _ = db.ExecContext(ctx, `ANALYZE`)
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func buildNotes(ctx context.Context, path string, rng *rand.Rand) (int64, error) {
	db, err := openDemo(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
CREATE TABLE folders (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE notes (
  id INTEGER PRIMARY KEY,
  folder_id INTEGER REFERENCES folders(id),
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  pinned INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);
CREATE TABLE tags (note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE, tag TEXT NOT NULL, PRIMARY KEY (note_id, tag));`); err != nil {
		return 0, err
	}
	folders := []string{"Home", "Work", "Recipes", "Travel", "Ideas"}
	for i, f := range folders {
		if _, err := db.ExecContext(ctx, `INSERT INTO folders (id, name) VALUES (?, ?)`, i+1, f); err != nil {
			return 0, err
		}
	}
	titles := []string{"Shopping list", "Plan for the weekend", "Book recommendations", "Server upgrade notes", "Ćevapi at home", "Trip to Mostar", "Garden ideas",
		"Meeting notes", "Gift ideas", "Movies to watch", "Bike maintenance", "Bread recipe", "Packing list", "Questions for the doctor"}
	tags := []string{"todo", "later", "family", "work", "food", "travel"}
	for i := 1; i <= 64; i++ {
		at := time.Now().Add(-time.Duration(rng.Intn(24*200)) * time.Hour).UTC().Format("2006-01-02 15:04:05")
		if _, err := db.ExecContext(ctx, `INSERT INTO notes (id, folder_id, title, body, pinned, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, i, 1+rng.Intn(len(folders)),
			titles[rng.Intn(len(titles))], "Written on "+at[:10]+". "+strings.Repeat("Some thoughts to keep. ", 1+rng.Intn(8)), rng.Intn(9) == 0, at); err != nil {
			return 0, err
		}
		for _, tg := range tags {
			if rng.Intn(5) == 0 {
				if _, err := db.ExecContext(ctx, `INSERT INTO tags (note_id, tag) VALUES (?, ?)`, i, tg); err != nil {
					return 0, err
				}
			}
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func asciiName(s string) string {
	return strings.NewReplacer("č", "c", "ć", "c", "š", "s", "ž", "z", "đ", "dj", "Č", "C", "Ć", "C", "Š", "S", "Ž", "Z").Replace(s)
}
