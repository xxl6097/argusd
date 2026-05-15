package argus

// OUIDatabase maps 24-bit OUI prefixes (uppercase, no separators) to
// vendor short names. Curated from IEEE OUI registry for home LAN
// coverage — see oui.go for the rationale.
//
// Override this map at startup to inject your own data:
//
//	argus.OUIDatabase["AA:BB:CC"] = "MyCorp"  // (use the 6-char form)
//
// Data source: IEEE OUI registry (https://standards-oui.ieee.org/oui/oui.csv),
// hand-picked subset. Last refresh: 2026-05-15.
//
// Stable since v1.2.0. The map type itself is part of the Stable surface;
// individual entries may be added or refined in minor releases without
// notice (additions are non-breaking by definition).
var OUIDatabase = map[string]string{
	// --- Apple (covers iPhone / iPad / Mac / Apple Watch / AirPods) ---
	"001451": "Apple", "0017F2": "Apple", "001B63": "Apple", "001CB3": "Apple",
	"001D4F": "Apple", "001E52": "Apple", "001EC2": "Apple", "001F5B": "Apple",
	"001FF3": "Apple", "00214F": "Apple", "0021E9": "Apple", "002241": "Apple",
	"002332": "Apple", "0023DF": "Apple", "002500": "Apple", "002608": "Apple",
	"00264A": "Apple", "0026B0": "Apple", "0026BB": "Apple",
	"0CD746": "Apple", "0C8525": "Apple", "10417F": "Apple", "1093E9": "Apple",
	"3C0754": "Apple", "3C2EFF": "Apple", "404D7F": "Apple", "44D884": "Apple",
	"60FACD": "Apple", "68967B": "Apple", "8C8590": "Apple", "9C20BB": "Apple",
	"A4D1D2": "Apple", "AC293A": "Apple", "B844D9": "Apple", "BC926B": "Apple",
	"D02B20": "Apple", "DC2B61": "Apple", "F0B479": "Apple", "F40F24": "Apple",
	"F8FFC2": "Apple", "F4F15A": "Apple",

	// --- Xiaomi / Mi ecosystem (covers phones, MIJIA, smart home, AIoT) ---
	"04CF8C": "Xiaomi", "286C07": "Xiaomi", "342DC4": "Xiaomi", "3480B3": "Xiaomi",
	"5475D0": "Xiaomi", "64B473": "Xiaomi", "742344": "Xiaomi", "7C1DD9": "Xiaomi",
	"843835": "Xiaomi", "8CBEBE": "Xiaomi", "8CDE52": "Xiaomi", "98FAE3": "Xiaomi",
	"9C99A0": "Xiaomi", "9CB6D0": "Xiaomi", "A0860F": "Xiaomi", "B47C9C": "Xiaomi",
	"B888D5": "Xiaomi", "C40BCB": "Xiaomi", "C46AB7": "Xiaomi", "D4359C": "Xiaomi",
	"D45D64": "Xiaomi", "EC4181": "Xiaomi", "F0B429": "Xiaomi", "F48B32": "Xiaomi",
	"F8A45F": "Xiaomi", "FC64BA": "Xiaomi",

	// --- Samsung ---
	"001632": "Samsung", "001D25": "Samsung", "001E7D": "Samsung", "001FCC": "Samsung",
	"0023D7": "Samsung", "002566": "Samsung", "0026E1": "Samsung", "0CDFA4": "Samsung",
	"1C5A3E": "Samsung", "30C7AE": "Samsung", "388B59": "Samsung", "5440AD": "Samsung",
	"7CF90E": "Samsung", "8C77124": "Samsung", "94350A": "Samsung", "B8C68E": "Samsung",
	"E8508B": "Samsung", "F0E77E": "Samsung",

	// --- Huawei / Honor ---
	"001E10": "Huawei", "002568": "Huawei", "002EC7": "Huawei", "00464B": "Huawei",
	"04F938": "Huawei", "087A4C": "Huawei", "20A680": "Huawei", "28E347": "Huawei",
	"4CB16C": "Huawei", "5CC9D3": "Huawei", "70723C": "Huawei", "780CB8": "Huawei",
	"7C7635": "Huawei", "844765": "Huawei", "B05B0E": "Huawei", "BC25E0": "Huawei",
	"D03E5C": "Huawei", "E84DD0": "Huawei", "F4B8A7": "Huawei",

	// --- Google (Pixel, Nest, Chromecast) ---
	"007D44": "Google", "0492FB": "Google", "1C24CD": "Google", "20DF3F": "Google",
	"40A6D9": "Google", "54603B": "Google", "5CD0E2": "Google", "6466B3": "Google",
	"6C2495": "Google", "94B7CD": "Google", "9C5C8E": "Google", "A4DA32": "Google",
	"AC6789": "Google", "AC9658": "Google", "C82A14": "Google", "D831CF": "Google",
	"E4FAFC": "Google", "F4F5D8": "Google", "F4F5E8": "Google",

	// --- Microsoft (Surface, Xbox, etc.) ---
	"00125A": "Microsoft", "0017FA": "Microsoft", "001DD8": "Microsoft", "0022F4": "Microsoft",
	"00E04C": "Realtek/Microsoft", "281878": "Microsoft", "44851F": "Microsoft", "60451B": "Microsoft",
	"7C1E52": "Microsoft", "98655A": "Microsoft", "98E7F4": "Microsoft", "C83F26": "Microsoft",

	// --- Intel (NICs, Wi-Fi cards) ---
	"001517": "Intel", "001CC0": "Intel", "001D09": "Intel", "001E64": "Intel",
	"002197": "Intel", "0025BA": "Intel", "0050F1": "Intel", "0427C7": "Intel",
	"0C8BFD": "Intel", "1098C3": "Intel", "1C697A": "Intel", "1CB72C": "Intel",
	"248A07": "Intel", "346F90": "Intel", "3C9509": "Intel", "44B0AA": "Intel",
	"4C7780": "Intel", "5824296": "Intel", "5C03BD": "Intel", "5CE0C5": "Intel",
	"6C883B": "Intel", "705A0F": "Intel", "7CB27D": "Intel", "8C5544": "Intel",
	"8CC841": "Intel", "8CF8C5": "Intel", "94DE80": "Intel", "9C7BEF": "Intel",
	"A4C3F0": "Intel", "AC7BA1": "Intel", "B40EDC": "Intel", "B4D5BD": "Intel",
	"C8348E": "Intel", "DC8B28": "Intel", "E03F49": "Intel", "F4D108": "Intel",

	// --- Realtek (cheap NICs, WiFi dongles) ---
	"0014D1": "Realtek", "001CDF": "Realtek", "001D0F": "Realtek", "001FE2": "Realtek",
	"40167E": "Realtek", "60A4B7": "Realtek", "8C2DAA": "Realtek", "9CC077": "Realtek",
	"DC4427": "Realtek", "F072EA": "Realtek", "F4060A": "Realtek",

	// --- Espressif (ESP32 / ESP8266 — the IoT / DIY universe) ---
	"24A160": "Espressif", "24B2DE": "Espressif", "246F28": "Espressif",
	"2C3AE8": "Espressif", "30C6F7": "Espressif", "3C6105": "Espressif",
	"3C71BF": "Espressif", "4022D8": "Espressif", "4C75259": "Espressif",
	"5CCF7F": "Espressif", "60019": "Espressif", "60019B": "Espressif",
	"68C63A": "Espressif", "78E36D": "Espressif", "84CCA8": "Espressif",
	"840D8E": "Espressif", "8CCE4E": "Espressif", "98F4AB": "Espressif",
	"9C9C1F": "Espressif", "A020A6": "Espressif", "AC0BFB": "Espressif",
	"AC67B2": "Espressif", "B4E62D": "Espressif", "BC9DA5": "Espressif",
	"BCDDC2": "Espressif", "BCFF4D": "Espressif", "C44F33": "Espressif",
	"C82E18": "Espressif", "C8C9A3": "Espressif", "C8DF84": "Espressif",
	"CC50E3": "Espressif", "D8A01D": "Espressif", "D8BFC0": "Espressif",
	"DC4F22": "Espressif", "E098060": "Espressif", "E098068": "Espressif",
	"EC94CB": "Espressif", "EC9420": "Espressif", "F09E9E": "Espressif",
	"F4CFA2": "Espressif", "FCF5C4": "Espressif",

	// --- Raspberry Pi Foundation (covers Pi 1-5, RPi-based boards) ---
	"B827EB": "Raspberry Pi", "DCA632": "Raspberry Pi", "E45F01": "Raspberry Pi",
	"2CCF67": "Raspberry Pi", "D83ADD": "Raspberry Pi",

	// --- Common IoT / camera / smart home ---
	"00FC8B": "Tuya",
	"04958E": "Sonos", "5CAAFD": "Sonos", "78282A": "Sonos", "B8E937": "Sonos",
	"244C9B": "Amazon", "44650D": "Amazon", "50DCE7": "Amazon", "68DBF5": "Amazon",
	"68F428": "Amazon", "747548": "Amazon", "78E103": "Amazon", "84D6D0": "Amazon",
	"A002DC": "Amazon", "A0D0DC": "Amazon", "AC63BE": "Amazon", "B47443": "Amazon",
	"B8E856": "Amazon", "F0272D": "Amazon", "F0F005": "Amazon", "FC65DE": "Amazon",
	"7C8BCA": "TP-Link", "00250E": "TP-Link", "60E327": "TP-Link",
	"AC84C6": "TP-Link", "B0BE76": "TP-Link", "C461F1": "TP-Link", "F44023": "TP-Link",
	"7CDDE9": "Ubiquiti", "002722": "Ubiquiti", "B4FBE4": "Ubiquiti", "DC9FDB": "Ubiquiti",
	"245EBE": "Synology", "0011328": "Synology",
	"504F94": "Honeywell",
	"D425CC": "ASUSTek", "00224D": "ASUSTek", "1C872C": "ASUSTek", "30852F": "ASUSTek",
	"4860BC": "ASUSTek", "AC22B": "ASUSTek", "BCEE7B": "ASUSTek",
	"FC34977": "ASUSTek",
	"00D29A": "TCL", "08F40F": "TCL", "BCB1F3": "TCL",
	"E891F4": "Tencent",
	"0017A4": "Lenovo", "001A6B": "Lenovo", "F8B156": "Lenovo",
	"402655": "Dell",

	// --- Networking / virtualisation infrastructure ---
	"080027": "VirtualBox",
	"00059A": "Cisco", "00163E": "Xen",
	"525400": "QEMU/KVM",
	// Docker default container MAC range: 02:42:ac:* — but bit 1 of the
	// first octet is set (locally-administered), so parseOUI returns
	// locally=true and we never reach the table. The router-side Docker
	// containers therefore correctly show as "—" in the UI.

	// --- Common Chinese OEMs / household ---
	"5C628B": "Vivo", "F0728C": "Vivo",
	"080078": "OPPO",
	"3C4A92": "OnePlus",
	"38B4D3": "Meizu", "78D752": "Meizu",
	"0023A7": "Redmi",
	"D8C4E9": "Realme",
	"34294F": "Anker",
	"40CD7A": "Roborock", "A081B5": "Roborock",
	"CC408E": "Ecovacs",
	"081196": "DJI",

	// --- Real-world prefixes seen on Xiaomi / Aqara / Mijia / Yeelight ecosystem ---
	// (verified live on a 2805 router in 2026-05; many of these aren't
	// in the canonical IEEE OUI registry under "Xiaomi" but are used by
	// IoT modules manufactured for the Mi ecosystem)
	"503123": "Xiaomi",  // xiaomi_vacuum_pv11cn (Mijia robot vacuum)
	"50B3B4": "Xiaomi",  // Mini7 (Mi soundbox) - Espressif sub-allocation
	"54EF44": "Xiaomi",  // Vela / Aqara hub / Mijia gateway (IEEE: Lumi)
	"B88880": "Xiaomi",  // xiaomi_camera + xiaomi-gateway-hub (Mi camera/gateway)
	"E4FE43": "Xiaomi",  // xiaomi-light-ceil (Mijia ceiling light)
	"34CE00": "Xiaomi",
	"48BEC1": "Xiaomi",
	"4480EB": "Xiaomi",
	"60D9C7": "Xiaomi",
	"6C5C14": "Xiaomi",
	"7811DC": "Xiaomi",
	"848352": "Xiaomi",
	"7C4953": "Xiaomi",  // xiaomi_lightbulb / Mi LED Smart Bulb

	"3CBD3E": "Nintendo", "B88AEC": "Nintendo",
	"0024BE": "Sony", "549F13": "Sony",
}
