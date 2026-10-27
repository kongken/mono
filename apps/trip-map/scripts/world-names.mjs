// Hand-maintained data for the world catalog: display names the source does not
// carry in the form the app wants, extra search aliases, and the ISO 3166-1
// entries the source has no polygon for. Everything else (names, continent, label
// point) comes straight from the pinned Natural Earth input.

// Common short Chinese names (the source uses long official forms for these).
export const ZH_NAMES = {
  CD: "刚果（金）",
  CG: "刚果（布）",
  CN: "中国",
  DO: "多米尼加共和国",
  FM: "密克罗尼西亚",
  GG: "根西岛",
  JE: "泽西岛",
  KP: "朝鲜",
  KR: "韩国",
  MN: "蒙古",
  AX: "奥兰群岛",
};

// English names where the source's NAME_LONG is not the common form.
export const EN_NAMES = {
  BN: "Brunei",
  CI: "Côte d'Ivoire",
  CV: "Cabo Verde",
  CZ: "Czechia",
  FK: "Falkland Islands",
  FO: "Faroe Islands",
  GM: "Gambia",
  KP: "North Korea",
  KR: "South Korea",
  LA: "Laos",
  MF: "Saint Martin (France)",
  RU: "Russia",
  SX: "Sint Maarten (Netherlands)",
  SZ: "Eswatini",
  VA: "Vatican City",
};

// Extra search aliases (Chinese and English). Source names that differ from the
// chosen name are added automatically.
export const ALIASES = {
  AE: ["阿联酋", "UAE"],
  BA: ["波黑"],
  CD: ["民主刚果", "DRC", "Congo-Kinshasa", "Congo Kinshasa"],
  CG: ["刚果", "Congo-Brazzaville", "Congo Brazzaville"],
  CI: ["象牙海岸", "Ivory Coast"],
  CN: ["中华人民共和国", "PRC", "香港", "澳门", "台湾", "Hong Kong", "Macao", "Macau", "Taiwan"],
  CV: ["Cape Verde", "佛得角共和国"],
  CZ: ["Czech Republic", "捷克共和国"],
  DE: ["Deutschland"],
  FK: ["Malvinas", "马尔维纳斯群岛"],
  GB: ["英格兰", "苏格兰", "大不列颠", "UK", "Britain", "Great Britain"],
  IR: ["伊朗伊斯兰共和国"],
  KP: ["朝鲜民主主义人民共和国", "北韩"],
  KR: ["大韩民国", "南韩", "Republic of Korea"],
  MK: ["马其顿", "Macedonia"],
  MM: ["Burma"],
  NL: ["Holland"],
  PS: ["巴勒斯坦国"],
  RU: ["俄罗斯联邦"],
  SY: ["叙利亚阿拉伯共和国"],
  SZ: ["Swaziland", "埃斯瓦蒂尼"],
  TL: ["East Timor"],
  TR: ["Türkiye", "Turkiye", "土耳其共和国"],
  US: ["美利坚合众国", "美利坚", "USA", "America", "United States of America"],
  VA: ["Holy See", "梵蒂冈城", "教廷"],
};

export const CONTINENTS = {
  AF: "非洲",
  AN: "南极洲",
  AS: "亚洲",
  EU: "欧洲",
  NA: "北美洲",
  OC: "大洋洲",
  SA: "南美洲",
};

// The source's CONTINENT value for each entry; "Seven seas (open ocean)" is
// resolved per entry below.
export const SOURCE_CONTINENT = {
  Africa: "AF",
  Antarctica: "AN",
  Asia: "AS",
  Europe: "EU",
  "North America": "NA",
  Oceania: "OC",
  "South America": "SA",
};
export const OPEN_OCEAN_CONTINENT = { GS: "AN", TF: "AN", HM: "AN", IO: "AS", SH: "AF", SC: "AF", MU: "AF", MV: "AS" };

// ISO 3166-1 entries Natural Earth 1:50m has no polygon of its own for (they sit
// inside another feature or are too small). They stay in the catalog with
// hasShape:false so they are still searchable and selectable; `point` is only a
// label position, never a city coordinate.
export const NO_SHAPE = [
  { code: "BQ", name: "荷属加勒比区", en: "Caribbean Netherlands", aliases: ["博奈尔", "Bonaire"], continent: "NA", point: [-68.26, 12.18] },
  { code: "BV", name: "布韦岛", en: "Bouvet Island", aliases: [], continent: "AN", point: [3.36, -54.42] },
  { code: "CC", name: "科科斯（基林）群岛", en: "Cocos (Keeling) Islands", aliases: ["科科斯群岛"], continent: "OC", point: [96.87, -12.16] },
  { code: "CX", name: "圣诞岛", en: "Christmas Island", aliases: [], continent: "OC", point: [105.63, -10.49] },
  { code: "GF", name: "法属圭亚那", en: "French Guiana", aliases: ["圭亚那（法属）"], continent: "SA", point: [-53.0, 4.0] },
  { code: "GI", name: "直布罗陀", en: "Gibraltar", aliases: [], continent: "EU", point: [-5.35, 36.14] },
  { code: "GP", name: "瓜德罗普", en: "Guadeloupe", aliases: [], continent: "NA", point: [-61.58, 16.25] },
  { code: "MQ", name: "马提尼克", en: "Martinique", aliases: [], continent: "NA", point: [-61.02, 14.64] },
  { code: "RE", name: "留尼汪", en: "Réunion", aliases: ["Reunion"], continent: "AF", point: [55.54, -21.11] },
  { code: "SJ", name: "斯瓦尔巴和扬马延", en: "Svalbard and Jan Mayen", aliases: ["斯瓦尔巴群岛", "Svalbard"], continent: "EU", point: [16.0, 78.0] },
  { code: "TK", name: "托克劳", en: "Tokelau", aliases: [], continent: "OC", point: [-171.82, -9.17] },
  { code: "UM", name: "美国本土外小岛屿", en: "U.S. Minor Outlying Islands", aliases: ["威克岛", "Wake Island"], continent: "OC", point: [166.65, 19.28] },
  { code: "YT", name: "马约特", en: "Mayotte", aliases: [], continent: "AF", point: [45.17, -12.82] },
];
