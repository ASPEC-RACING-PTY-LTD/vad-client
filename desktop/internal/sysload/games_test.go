package sysload

import "testing"

func TestGameName(t *testing.T) {
	extra := []string{`E:\Games\Ubi`}
	cases := map[string]string{
		`D:\SteamLibrary\steamapps\common\Cyberpunk 2077\bin\x64\Cyberpunk2077.exe`:                "Cyberpunk 2077",
		`C:\Program Files (x86)\Steam\steamapps\common\Counter-Strike Global Offensive\game\x.exe`: "Counter-Strike Global Offensive",
		`C:\Program Files\Epic Games\RocketLeague\Binaries\Win64\RocketLeague.exe`:                 "RocketLeague",
		`C:\Program Files (x86)\Epic Games\Launcher\Portal\Binaries\Win64\EpicGamesLauncher.exe`:   "",
		`C:\Program Files (x86)\Ubisoft\Ubisoft Game Launcher\games\Far Cry 6\bin\FarCry6.exe`:     "Far Cry 6",
		`E:\Games\Ubi\Anno 1800\Bin\Win64\Anno1800.exe`:                                            "Anno 1800",
		`C:\XboxGames\Forza Horizon 5\Content\ForzaHorizon5.exe`:                                   "Forza Horizon 5",
		`C:\Riot Games\Riot Client\RiotClientServices.exe`:                                         "",
		`C:\Riot Games\VALORANT\live\ShooterGame\Binaries\Win64\VALORANT-Win64-Shipping.exe`:       "VALORANT",
		`C:\Program Files (x86)\World of Warcraft\_retail_\Wow.exe`:                                "World of Warcraft",
		`D:\SteamLibrary\steamapps\common\wallpaper_engine\wallpaper64.exe`:                        "",
		`D:\SteamLibrary\steamapps\common\Some Game\_CommonRedist\vcredist\vc_redist.x64.exe`:      "",
		`D:\SteamLibrary\steamapps\common\Some Game\UnityCrashHandler64.exe`:                       "",
		`C:\Program Files\VaultDock\VaultDock.exe`:                                                 "",
		`C:\Windows\explorer.exe`: "",
		``:                        "",
	}
	for exe, want := range cases {
		if got := GameName(exe, extra); got != want {
			t.Errorf("GameName(%q) = %q, want %q", exe, got, want)
		}
	}
}

func TestRunningGameScan(t *testing.T) {
	// Lists the processes of this machine; must not fail or hang.
	t.Logf("running game: %q", runningGame())
}
