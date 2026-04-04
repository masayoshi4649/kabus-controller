package main

// kabus APIで提供されている先物銘柄の情報を表す構造体
type FutureCode struct {
	FutureCode string `json:"future_code"`
	FutureName string `json:"future_name"`
}

// kabus APIで提供されている先物銘柄のリスト
var FutureProducts = []FutureCode{
	{FutureCode: "NK225", FutureName: "日経平均先物"},
	{FutureCode: "NK225mini", FutureName: "日経225mini先物"},
	{FutureCode: "TOPIX", FutureName: "TOPIX先物"},
	{FutureCode: "TOPIXmini", FutureName: "ミニTOPIX先物"},
	{FutureCode: "GROWTH", FutureName: "グロース250先物"},
	{FutureCode: "JPX400", FutureName: "JPX日経400先物"},
	{FutureCode: "DOW", FutureName: "NYダウ先物"},
	{FutureCode: "VI", FutureName: "日経平均VI先物"},
	{FutureCode: "Core30", FutureName: "TOPIX Core30先物"},
	{FutureCode: "REIT", FutureName: "東証REIT指数先物"},
	{FutureCode: "NK225micro", FutureName: "日経225マイクロ先物"},
}

type OptionCode struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var OptionProducts = []OptionCode{
	{Code: "NK225op", Name: "日経225オプション"},
	{Code: "NK225miniop", Name: "日経225ミニオプション"},
}
