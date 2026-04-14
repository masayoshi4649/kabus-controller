package main

// FutureCode は kabus API で提供される先物銘柄コード情報を表す。
type FutureCode struct {
	FutureCode string `json:"future_code"`
	FutureName string `json:"future_name"`
}

// FutureProducts は kabus API で扱う主要な先物銘柄コード一覧である。
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

// OptionCode は kabus API で提供されるオプション銘柄コード情報を表す。
type OptionCode struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// OptionProducts は kabus API で扱う主要なオプション銘柄コード一覧である。
var OptionProducts = []OptionCode{
	{Code: "NK225op", Name: "日経225オプション"},
	{Code: "NK225miniop", Name: "日経225ミニオプション"},
}
