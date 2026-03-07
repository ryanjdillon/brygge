package main

type boatModelSeed struct {
	manufacturer string
	model        string
	yearFrom     *int
	yearTo       *int
	lengthM      *float64
	beamM        *float64
	draftM       *float64
	weightKg     *float64
	boatType     string
}

func intP(v int) *int         { return &v }
func floatP(v float64) *float64 { return &v }

var seedBoatModels = []boatModelSeed{
	// -- Norwegian motorboats --
	{"Askeladden", "C61 Center", intP(2020), nil, floatP(6.15), floatP(2.28), floatP(0.40), floatP(1050), "motorboat"},
	{"Askeladden", "C61 Cruiser", intP(2020), nil, floatP(6.15), floatP(2.28), floatP(0.40), floatP(1100), "motorboat"},
	{"Askeladden", "P66 Weekend", intP(2018), nil, floatP(6.58), floatP(2.48), floatP(0.50), floatP(1500), "motorboat"},
	{"Askeladden", "C83 Cruiser", intP(2021), nil, floatP(8.30), floatP(2.70), floatP(0.55), floatP(2800), "motorboat"},
	{"Askeladden", "P76 Weekend", intP(2019), nil, floatP(7.60), floatP(2.55), floatP(0.50), floatP(2200), "motorboat"},
	{"Nordkapp", "Enduro 605", intP(2020), nil, floatP(6.10), floatP(2.30), floatP(0.40), floatP(1100), "motorboat"},
	{"Nordkapp", "Enduro 705", intP(2020), nil, floatP(7.10), floatP(2.50), floatP(0.45), floatP(1600), "motorboat"},
	{"Nordkapp", "Noblesse 660", intP(2019), nil, floatP(6.60), floatP(2.48), floatP(0.45), floatP(1400), "motorboat"},
	{"Nordkapp", "Noblesse 820", intP(2019), nil, floatP(8.20), floatP(2.70), floatP(0.55), floatP(2500), "motorboat"},
	{"Ibiza", "22 Touring", intP(2015), nil, floatP(6.50), floatP(2.40), floatP(0.40), floatP(1100), "motorboat"},
	{"Ibiza", "24 HT", intP(2016), nil, floatP(7.30), floatP(2.50), floatP(0.45), floatP(1800), "motorboat"},
	{"Marex", "310 Sun Cruiser", intP(2018), nil, floatP(9.55), floatP(3.20), floatP(0.80), floatP(4500), "motorboat"},
	{"Marex", "320 Aft Cabin Cruiser", intP(2019), nil, floatP(9.95), floatP(3.20), floatP(0.85), floatP(5200), "motorboat"},
	{"Marex", "375", intP(2021), nil, floatP(11.50), floatP(3.60), floatP(0.95), floatP(7500), "motorboat"},
	{"Nimbus", "305 Coupé", intP(2017), nil, floatP(9.49), floatP(3.15), floatP(0.88), floatP(4200), "motorboat"},
	{"Nimbus", "365 Coupé", intP(2020), nil, floatP(11.10), floatP(3.49), floatP(0.95), floatP(6800), "motorboat"},
	{"Nimbus", "T11", intP(2022), nil, floatP(11.35), floatP(3.36), floatP(0.80), floatP(5600), "motorboat"},

	// -- Swedish / Finnish motorboats --
	{"Yamarin", "60 DC", intP(2020), nil, floatP(5.95), floatP(2.25), floatP(0.35), floatP(850), "motorboat"},
	{"Yamarin", "63 DC", intP(2021), nil, floatP(6.34), floatP(2.33), floatP(0.38), floatP(980), "motorboat"},
	{"Yamarin", "68 DC", intP(2020), nil, floatP(6.75), floatP(2.48), floatP(0.42), floatP(1300), "motorboat"},
	{"Yamarin", "88 DC", intP(2022), nil, floatP(8.80), floatP(2.75), floatP(0.55), floatP(2800), "motorboat"},
	{"Buster", "L", intP(2015), nil, floatP(4.63), floatP(1.85), floatP(0.30), floatP(350), "motorboat"},
	{"Buster", "M", intP(2015), nil, floatP(4.28), floatP(1.70), floatP(0.28), floatP(270), "motorboat"},
	{"Buster", "XL", intP(2018), nil, floatP(5.35), floatP(2.07), floatP(0.35), floatP(530), "motorboat"},
	{"Buster", "XXL", intP(2020), nil, floatP(6.25), floatP(2.30), floatP(0.40), floatP(900), "motorboat"},
	{"Quicksilver", "505 Open", intP(2018), nil, floatP(5.05), floatP(2.03), floatP(0.32), floatP(550), "motorboat"},
	{"Quicksilver", "675 Weekend", intP(2019), nil, floatP(6.43), floatP(2.48), floatP(0.45), floatP(1350), "motorboat"},

	// -- Small boats / dinghies --
	{"Pioner", "10", intP(2000), nil, floatP(3.10), floatP(1.40), floatP(0.20), floatP(55), "small"},
	{"Pioner", "12", intP(2000), nil, floatP(3.70), floatP(1.55), floatP(0.22), floatP(72), "small"},
	{"Pioner", "14", intP(2000), nil, floatP(4.24), floatP(1.72), floatP(0.25), floatP(110), "small"},
	{"Pioner", "15", intP(2005), nil, floatP(4.60), floatP(1.82), floatP(0.28), floatP(130), "small"},
	{"Ryds", "486 BF", intP(2015), nil, floatP(4.80), floatP(1.87), floatP(0.28), floatP(380), "motorboat"},
	{"Ryds", "550 GT", intP(2018), nil, floatP(5.48), floatP(2.10), floatP(0.35), floatP(620), "motorboat"},
	{"Terhi", "400", intP(2010), nil, floatP(4.00), floatP(1.55), floatP(0.22), floatP(80), "small"},
	{"Terhi", "475 BR", intP(2015), nil, floatP(4.72), floatP(1.88), floatP(0.28), floatP(380), "motorboat"},
	{"Rana", "17", intP(2010), nil, floatP(5.15), floatP(2.00), floatP(0.30), floatP(450), "motorboat"},
	{"Rana", "19", intP(2012), nil, floatP(5.70), floatP(2.15), floatP(0.35), floatP(600), "motorboat"},

	// -- European sailboats --
	{"Jeanneau", "Sun Odyssey 319", intP(2018), nil, floatP(9.97), floatP(3.28), floatP(1.65), floatP(4300), "sailboat"},
	{"Jeanneau", "Sun Odyssey 349", intP(2015), nil, floatP(10.34), floatP(3.44), floatP(1.98), floatP(5500), "sailboat"},
	{"Jeanneau", "Sun Odyssey 380", intP(2020), nil, floatP(11.08), floatP(3.69), floatP(1.98), floatP(6600), "sailboat"},
	{"Jeanneau", "Sun Odyssey 440", intP(2018), nil, floatP(13.39), floatP(4.29), floatP(2.15), floatP(9200), "sailboat"},
	{"Jeanneau", "Sun Odyssey 490", intP(2019), nil, floatP(14.42), floatP(4.49), floatP(2.25), floatP(11500), "sailboat"},
	{"Bavaria", "Cruiser 34", intP(2016), nil, floatP(10.36), floatP(3.42), floatP(1.95), floatP(5500), "sailboat"},
	{"Bavaria", "Cruiser 37", intP(2014), nil, floatP(11.30), floatP(3.67), floatP(1.85), floatP(7100), "sailboat"},
	{"Bavaria", "C42", intP(2020), nil, floatP(12.35), floatP(3.99), floatP(2.10), floatP(8800), "sailboat"},
	{"Bavaria", "C45", intP(2021), nil, floatP(13.70), floatP(4.35), floatP(2.15), floatP(10500), "sailboat"},
	{"Beneteau", "First 24", intP(2019), nil, floatP(7.49), floatP(2.54), floatP(1.50), floatP(1680), "sailboat"},
	{"Beneteau", "Oceanis 30.1", intP(2019), nil, floatP(9.53), floatP(3.18), floatP(1.68), floatP(3900), "sailboat"},
	{"Beneteau", "Oceanis 34.1", intP(2020), nil, floatP(10.47), floatP(3.49), floatP(1.80), floatP(5200), "sailboat"},
	{"Beneteau", "Oceanis 38.1", intP(2017), nil, floatP(11.50), floatP(3.99), floatP(2.08), floatP(6800), "sailboat"},
	{"Beneteau", "Oceanis 46.1", intP(2019), nil, floatP(14.60), floatP(4.51), floatP(2.23), floatP(11200), "sailboat"},
	{"Hallberg-Rassy", "340", intP(2011), nil, floatP(10.33), floatP(3.37), floatP(1.80), floatP(6100), "sailboat"},
	{"Hallberg-Rassy", "372", intP(2008), nil, floatP(11.35), floatP(3.60), floatP(1.85), floatP(8000), "sailboat"},
	{"Hallberg-Rassy", "400", intP(2014), nil, floatP(12.25), floatP(3.78), floatP(1.95), floatP(9200), "sailboat"},
	{"Hallberg-Rassy", "44", intP(2016), nil, floatP(13.48), floatP(4.12), floatP(2.05), floatP(12000), "sailboat"},
	{"Dehler", "30 OD", intP(2017), nil, floatP(9.44), floatP(2.98), floatP(1.95), floatP(3750), "sailboat"},
	{"Dehler", "34", intP(2016), nil, floatP(10.40), floatP(3.39), floatP(2.00), floatP(5300), "sailboat"},
	{"Dehler", "38", intP(2019), nil, floatP(11.48), floatP(3.65), floatP(2.10), floatP(7200), "sailboat"},
	{"Hanse", "315", intP(2016), nil, floatP(9.60), floatP(3.20), floatP(1.72), floatP(4600), "sailboat"},
	{"Hanse", "348", intP(2018), nil, floatP(10.72), floatP(3.48), floatP(1.85), floatP(5800), "sailboat"},
	{"Hanse", "388", intP(2017), nil, floatP(11.40), floatP(3.87), floatP(2.00), floatP(7200), "sailboat"},
	{"Hanse", "418", intP(2020), nil, floatP(12.40), floatP(4.17), floatP(2.10), floatP(8700), "sailboat"},
	{"Dufour", "310 Grand Large", intP(2015), nil, floatP(9.43), floatP(3.12), floatP(1.60), floatP(4200), "sailboat"},
	{"Dufour", "360 Grand Large", intP(2016), nil, floatP(10.73), floatP(3.52), floatP(1.80), floatP(6200), "sailboat"},
	{"Dufour", "390 Grand Large", intP(2020), nil, floatP(11.94), floatP(3.94), floatP(2.10), floatP(7800), "sailboat"},
	{"Nauticat", "331", intP(2005), nil, floatP(10.09), floatP(3.40), floatP(1.50), floatP(6300), "sailboat"},
	{"Nauticat", "385", intP(2008), nil, floatP(11.40), floatP(3.75), floatP(1.65), floatP(9400), "sailboat"},

	// -- Classic Norwegian sailboats --
	{"Maxi", "77", intP(1977), intP(1990), floatP(7.65), floatP(2.55), floatP(1.35), floatP(2100), "sailboat"},
	{"Maxi", "84", intP(1980), intP(1992), floatP(8.40), floatP(2.80), floatP(1.50), floatP(2900), "sailboat"},
	{"Maxi", "95", intP(1984), intP(1995), floatP(9.50), floatP(3.10), floatP(1.65), floatP(3800), "sailboat"},
	{"Albin", "Ballad", intP(1971), intP(1985), floatP(9.07), floatP(2.83), floatP(1.35), floatP(3100), "sailboat"},
	{"Albin", "Vega", intP(1966), intP(1979), floatP(8.25), floatP(2.46), floatP(1.15), floatP(2150), "sailboat"},
	{"IF", "International Folkboat", intP(1942), nil, floatP(7.65), floatP(2.22), floatP(1.19), floatP(1900), "sailboat"},
	{"Vindö", "32", intP(1970), intP(1988), floatP(9.50), floatP(2.90), floatP(1.50), floatP(4200), "sailboat"},
	{"Compromis", "999", intP(1985), intP(1998), floatP(9.99), floatP(3.15), floatP(1.55), floatP(4000), "sailboat"},
	{"Comfort", "30", intP(1978), intP(1995), floatP(9.10), floatP(2.98), floatP(1.50), floatP(3500), "sailboat"},
}
