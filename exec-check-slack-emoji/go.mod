module check-slack-emoji

go 1.19

//replace github.com/grammaton76/g76golib/shared => ../../g76golib/shared

//replace github.com/grammaton76/g76golib/chatoutput/sc_dbtable => ../../g76golib/chatoutput/sc_dbtable

require (
	github.com/go-resty/resty/v2 v2.7.0
	github.com/grammaton76/g76golib/chatoutput/sc_dbtable v0.0.0-20220919102826-4d9ab69ab0cf
	github.com/grammaton76/g76golib/shared v0.0.0-20220919102826-4d9ab69ab0cf
	github.com/grammaton76/g76golib/sjson v0.0.0-20220919102826-4d9ab69ab0cf
	github.com/grammaton76/g76golib/slogger v0.0.0-20220919102826-4d9ab69ab0cf
)

require (
	github.com/VividCortex/mysqlerr v1.0.0 // indirect
	github.com/fsnotify/fsnotify v1.4.7 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-sql-driver/mysql v1.6.0 // indirect
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0 // indirect
	github.com/lib/pq v1.10.6 // indirect
	github.com/papertrail/go-tail v0.0.0-20180509224916-973c153b0431 // indirect
	github.com/shopspring/decimal v1.3.1 // indirect
	golang.org/x/net v0.0.0-20211029224645-99673261e6eb // indirect
	golang.org/x/sys v0.0.0-20220319134239-a9b59b0215f8 // indirect
)
