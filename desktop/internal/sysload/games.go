package sysload

import (
	"path/filepath"
	"strings"
)

// Game detection: a running program counts as a game when its executable is
// inside a game library of a store or launcher (Steam, Epic Games, Ubisoft
// Connect, EA app, GOG Galaxy, Battle.net, Xbox app, Riot, Rockstar, Amazon
// Games, itch), whatever drive the library is on. Launchers, their helpers
// and redistributable installers are not games.

// libraryMarkers are path fragments (lower case, backslashes) that only
// appear inside a game library.
var libraryMarkers = []string{
	`\steamapps\common\`,
	`\epic games\`,
	`\ubisoft game launcher\games\`,
	`\ubisoft\ubisoft game launcher\games\`,
	`\ea games\`,
	`\origin games\`,
	`\gog galaxy\games\`,
	`\gog games\`,
	`\xboxgames\`,
	`\riot games\`,
	`\rockstar games\`,
	`\amazon games\library\`,
	`\itch\apps\`,
	`\battle.net\games\`,
}

// notGames are path fragments of launchers, overlays, tools and installers
// that live inside libraries but are not games.
var notGames = []string{
	`\epic games\launcher\`,
	`\epic games\epic online services\`,
	`\epicgameslauncher`,
	`\riot games\riot client\`,
	`\rockstar games\launcher\`,
	`\rockstar games\social club\`,
	`\steamapps\common\steamworks shared\`,
	`\steamapps\common\steam controller configs\`,
	`\steamapps\common\wallpaper_engine\`,
	`\steamapps\common\lossless scaling\`,
	`\steamapps\common\soundpad\`,
	`\steamapps\common\steamvr\`,
	`\_commonredist\`,
	`\redist\`,
	`\directx\`,
	`\easyanticheat\`,
	`\battleye\`,
}

// notGameExes are helper programs found next to games.
var notGameExes = map[string]bool{
	"unitycrashhandler64.exe": true, "unitycrashhandler32.exe": true, "crashreportclient.exe": true,
	"crashpad_handler.exe": true, "easyanticheat.exe": true, "easyanticheat_eos.exe": true,
	"easyanticheat_eos_setup.exe": true, "beservice.exe": true, "beservice_x64.exe": true,
	"vc_redist.x64.exe": true, "vc_redist.x86.exe": true, "dxsetup.exe": true, "dotnetfx.exe": true,
	"uninstall.exe": true, "unins000.exe": true, "uplay_bootstrapper.exe": true,
}

// knownGameExes are games whose launchers install them in ordinary program
// folders (mostly Battle.net), matched by executable name.
var knownGameExes = map[string]string{
	"wow.exe": "World of Warcraft", "wowclassic.exe": "World of Warcraft Classic",
	"overwatch.exe": "Overwatch 2", "diablo iv.exe": "Diablo IV", "diablo iii64.exe": "Diablo III",
	"hearthstone.exe": "Hearthstone", "heroesofthestorm_x64.exe": "Heroes of the Storm",
	"sc2_x64.exe": "StarCraft II", "starcraft.exe": "StarCraft", "cod.exe": "Call of Duty",
	"valorant-win64-shipping.exe": "VALORANT", "league of legends.exe": "League of Legends",
	"fortniteclient-win64-shipping.exe": "Fortnite", "r5apex.exe": "Apex Legends",
	"cs2.exe": "Counter-Strike 2", "dota2.exe": "Dota 2", "gta5.exe": "Grand Theft Auto V",
	"rdr2.exe": "Red Dead Redemption 2", "minecraft.windows.exe": "Minecraft",
	"robloxplayerbeta.exe": "Roblox", "eldenring.exe": "Elden Ring",
}

// GameName returns the game a running executable belongs to ("" when it is
// not a game). extra are additional library folders (lower case is not
// required), for example custom install folders read from launcher settings.
func GameName(exe string, extra []string) string {
	if exe == "" {
		return ""
	}
	p := strings.ToLower(strings.ReplaceAll(exe, "/", `\`))
	base := filepath.Base(strings.ReplaceAll(p, `\`, "/"))
	if notGameExes[base] {
		return ""
	}
	if name, ok := knownGameExes[base]; ok {
		return name
	}
	for _, n := range notGames {
		if strings.Contains(p, n) {
			return ""
		}
	}
	for _, m := range libraryMarkers {
		if i := strings.Index(p, m); i >= 0 {
			return folderName(exe, i+len(m))
		}
	}
	for _, d := range extra {
		d = strings.ToLower(strings.TrimRight(strings.ReplaceAll(d, "/", `\`), `\`)) + `\`
		if len(d) > 3 && strings.HasPrefix(p, d) {
			return folderName(exe, len(d)-1)
		}
	}
	return ""
}

// folderName returns the first folder of exe after offset i (the game's own
// folder inside the library), or the executable name.
func folderName(exe string, i int) string {
	rest := strings.TrimLeft(strings.ReplaceAll(exe, "/", `\`)[min(i, len(exe)):], `\`)
	if j := strings.IndexByte(rest, '\\'); j > 0 {
		return rest[:j]
	}
	return strings.TrimSuffix(filepath.Base(strings.ReplaceAll(exe, `\`, "/")), filepath.Ext(exe))
}
