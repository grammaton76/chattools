module chatcli

go 1.18

replace github.com/grammaton76/g76golib/pkg/sjson => ../../../g76golib/pkg/sjson

replace github.com/grammaton76/chattools/pkg/chat_output/sc_dbtable => ../../pkg/chat_output/sc_dbtable

require (
	github.com/grammaton76/chattools/pkg/chat_output/sc_dbtable v0.0.0-20221028045721-83ccc654b8d3
	github.com/grammaton76/g76golib/pkg/shared v0.0.0-20221028045618-a4c734ae155b
	github.com/grammaton76/g76golib/pkg/sjson v0.0.0-00010101000000-000000000000
	github.com/grammaton76/g76golib/pkg/slogger v0.0.0-20221028045618-a4c734ae155b
)

require (
	github.com/VividCortex/mysqlerr v1.0.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fsnotify/fsnotify v1.4.7 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-sql-driver/mysql v1.6.0 // indirect
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0 // indirect
	github.com/lib/pq v1.10.6 // indirect
	github.com/papertrail/go-tail v0.0.0-20180509224916-973c153b0431 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/shopspring/decimal v1.3.1 // indirect
	golang.org/x/sys v0.0.0-20220919091848-fb04ddd9f9c8 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
