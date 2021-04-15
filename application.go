package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	collection "github.com/mrjosh/go-collection"
	"github.com/mrjosh/pepebot/api"
	"github.com/mrjosh/pepebot/api/dota2/responses"
	"github.com/mrjosh/pepebot/components"
	"github.com/mrjosh/pepebot/config"
	"github.com/mrjosh/pepebot/db"
	"github.com/mrjosh/pepebot/disc"
	"github.com/mrjosh/pepebot/messages"
	"github.com/mrjosh/pepebot/models"
	cmap "github.com/orcaman/concurrent-map"
	"github.com/polds/imgbase64"
)

type GameEndChannel struct {
	Won     bool
	MatchId string
	GuildId string
}

type Application struct {
	Client           *discordgo.Session
	GsiChannel       chan *components.GSIResponse
	GameEndChannel   chan *GameEndChannel
	GuildLiveMatches cmap.ConcurrentMap
}

func (a *Application) GetGuildMatch(guildID string) (*components.GuildMatch, bool) {
	guildMatch, ok := a.GuildLiveMatches.Get(guildID)
	if ok {
		return guildMatch.(*components.GuildMatch), ok
	}
	return nil, false
}

func (a *Application) ConnectToAuthorVoiceChannel(dg *models.Guild, msg *discordgo.MessageCreate) error {
	if _, ok := a.GetGuildMatch(msg.GuildID); !ok {

		guild, _ := a.Client.Guild(msg.GuildID)
		for _, vs := range guild.VoiceStates {
			if vs.UserID == msg.Author.ID {

				vChannel := &disc.Channel{
					ID:     vs.ChannelID,
					Client: a.Client,
				}
				voiceConnection, err := vChannel.Join()
				if err != nil {
					return err
				}

				a.GuildLiveMatches.Set(msg.GuildID, &components.GuildMatch{
					VoiceConnection: voiceConnection,
					Guild:           dg,
					DiscordGuild:    guild,
					Runes:           components.NewRunes(),
				})

			}
		}
	}
	return nil
}

func (a *Application) HandleMessages(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.ID == s.State.User.ID {
		return
	}

	if strings.HasPrefix(m.Content, "-") {

		prefixCommand := strings.Split(strings.TrimSpace(m.Content), "-")
		args := strings.Split(prefixCommand[1], " ")

		switch args[0] {
		case "about":
			messages.SendAboutTextWithMessageCreate(s, m)
			return
		case "help":
			messages.SendHelpTextWithMessageCreate(s, m)
			return
		case "instructions":
			messages.SendInstructionsWithMessageCreate(s, m)
			return
		case "main_voice_channel":
			messages.SetMainVoiceChannelWithMessageCreate(args, s, m)
			return
		case "main_text_channel":
			messages.SetMainTextChannelWithMessageCreate(args, s, m)
			return
		case "match_history":
			messages.ShowMatchHistoryWithMessageCreate(args, s, m)
			return
		}
	}

}

func (a *Application) ConnectToVoiceChannelIfNotConnected(gameMatch *components.GSIResponse) error {
	if _, ok := a.GetGuildMatch(gameMatch.DiscordGuild.ID); !ok {
		// connect to main voice channel of the guild
		if gameMatch.Guild.MainVoiceChannelID != "" {
			vChannel := &disc.Channel{
				ID:     gameMatch.Guild.MainVoiceChannelID,
				Client: a.Client,
			}
			voiceConnection, err := vChannel.Join()
			if err != nil {
				return err
			}

			guildMatch := &components.GuildMatch{
				VoiceConnection: voiceConnection,
				Guild:           gameMatch.Guild,
				DiscordGuild:    gameMatch.DiscordGuild,
				Runes:           components.NewRunes(),
			}
			a.GuildLiveMatches.Set(gameMatch.DiscordGuild.ID, guildMatch)
		}
	}
	return nil
}

func (a *Application) CheckRunes() {

	for {
		select {
		case gameMatch := <-a.GsiChannel:

			switch gameMatch.Map.GameState {

			case components.PreGame:
				if err := a.ConnectToVoiceChannelIfNotConnected(gameMatch); err != nil {
					continue
				}
				break

			case components.StrategyTime:
				break
			case components.HeroSelection:
				break
			case components.WaitForMapToLoad:
				break
			case components.WaitForPlayersToLoad:
				break
			case components.InProgress:

				if gm, ok := a.GetGuildMatch(gameMatch.DiscordGuild.ID); ok {

					if gm.GameEnded {
						gm.GameEnded = false
					}

					gm.Runes.ClockTime = strconv.Itoa(gameMatch.Map.ClockTime)
					if ok, clock, _ := gm.Runes.Up(); ok {
						if coll := collection.New(gm.Runes.RuneTimes); !coll.Has(clock) {
							gm.Runes.RuneTimes = append(gm.Runes.RuneTimes, clock)
							gm.PlaySound(gm.Runes.GetRandomVoiceFileName())
						}
					}

				}

				break
			case components.PostGame:

				if gm, ok := a.GetGuildMatch(gameMatch.DiscordGuild.ID); ok {

					if !gm.GameEnded {
						if gameMatch.Map.WinTeam != "none" && gameMatch.Map.WinTeam != "" {

							endStruct := &GameEndChannel{
								MatchId: gameMatch.Map.Matchid,
								Won:     false,
								GuildId: gameMatch.DiscordGuild.ID,
							}

							if gameMatch.Player.TeamName != gameMatch.Map.WinTeam {
								gm.PlaySound(a.getRandomLossSound())
							} else {
								endStruct.Won = true
								gm.PlaySound(a.getRandomWinSound())
							}

							gm.Runes.RuneTimes = []string{}
							gm.Runes.ClockTime = ""
							gm.GameEnded = true
							a.GameEndChannel <- endStruct
						}
					}

				}

				break
			}
		}
	}

}

func (a *Application) getRandomLossSound() string {
	source := rand.NewSource(time.Now().Unix())
	random := rand.New(source)
	sounds := config.Map.Sounds.Loss
	return sounds[random.Intn(len(sounds))]
}

func (a *Application) getRandomWinSound() string {
	source := rand.NewSource(time.Now().Unix())
	random := rand.New(source)
	sounds := config.Map.Sounds.Win
	return sounds[random.Intn(len(sounds))]
}

func (a *Application) CheckGameEndStatus() {
	for {
		select {
		case gameMatch := <-a.GameEndChannel:

			if gm, ok := a.GetGuildMatch(gameMatch.GuildId); ok {
				if gm.HasVoiceConnection() {
					gm.VoiceConnection.Disconnect()
				}
				//msg, _ := api.GetMatchHistory(gameMatch.MatchId, true, gameMatch.Won, true, a.Client, gm.DiscordGuild)
				msg, _ := api.GetMatchHistory(gameMatch.MatchId, true, gameMatch.Won, true, a.Client)
				a.Client.ChannelMessageSend(gm.Guild.MainTextChannelID, msg)
				a.GuildLiveMatches.Remove(gm.DiscordGuild.ID)
			}

		}
	}
}

func (a *Application) RegisterAndServeBot() {

	if err := responses.FeatchHeroes(); err != nil {
		log.Panicf("Could not featch heroes from steam web api :%v", err)
	}

	if err := db.Configure(); err != nil {
		log.Panicf("Could not connect to database :%v", err)
	}

	dbModels := []interface{}{
		&models.Guild{},
		&models.Player{},
	}

	if err := db.Connection.AutoMigrate(dbModels...); err != nil {
		log.Panicf("Could not connect to database :%v", err)
	}

	discord, err := discordgo.New(fmt.Sprintf("Bot %s", config.Map.Discord.Token))
	if err != nil {
		log.Panicf("Could not connect to discord :%v", err)
	}
	a.Client = discord

	discord.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		commandHandlers := map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
			"help":               messages.SendHelpText,
			"match_history":      messages.ShowMatchHistory,
			"about":              messages.SendAboutText,
			"instructions":       messages.SendInstructions,
			"main_text_channel":  messages.SetMainTextChannel,
			"main_voice_channel": messages.SetMainVoiceChannel,
			"player_add":         messages.AddPlayer,
			"player_remove":      messages.RemovePlayer,
			"join":               messages.ConnectVoiceChannel(a.GuildLiveMatches),
			"leave":              messages.DisconnectVoiceChannel(a.GuildLiveMatches),
		}
		if h, ok := commandHandlers[i.Data.Name]; ok {
			h(s, i)
		}
	})

	discord.AddHandler(func(s *discordgo.Session, event *discordgo.Disconnect) {
		log.Println("Disconnected from discord!")
	})

	discord.AddHandler(func(s *discordgo.Session, event *discordgo.Ready) {
		log.Println("Logged in as !" + event.User.ID)
		commands := []*discordgo.ApplicationCommand{
			{
				Name:        "help",
				Description: "Shows information about pepebot",
			},
			{
				Name:        "join",
				Description: "Sommons the bot to your voice channel",
			},
			{
				Name:        "leave",
				Description: "Leave the voice channel",
			},
			{
				Name:        "about",
				Description: "Shows about pepebot",
			},
			{
				Name:        "instructions",
				Description: "Shows instructions about how to setup pepebot for your own discord server",
			},
			{
				Name:        "match_history",
				Description: "Shows summary of a dota2 match",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionInteger,
						Name:        "match_id",
						Description: "Dota 2 match id",
						Required:    true,
					},
				},
			},
			{
				Name:        "main_text_channel",
				Description: "Set main_text_channel for match summaries at the end of every game",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "channel_id",
						Description: "Text channel id",
						Required:    true,
					},
				},
			},
			{
				Name:        "main_voice_channel",
				Description: "Set main_voice_channel for pepebot to join every game to remind the runes",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "channel_id",
						Description: "Voice channel id",
						Required:    true,
					},
				},
			},
			{
				Name:        "player_add",
				Description: "Assing user's steam id",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "The discord user",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "steam_account_id",
						Description: "User's steam account id",
						Required:    true,
					},
				},
			},
			{
				Name:        "player_remove",
				Description: "Disconnect a player",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "The discord user",
						Required:    true,
					},
				},
			},
		}

		for _, cmd := range commands {
			if _, err := discord.ApplicationCommandCreate(discord.State.User.ID, "", cmd); err != nil {
				log.Panicf("Cannot create '%v' command: %v", cmd.Name, err)
			}
		}

	})

	discord.AddHandler(a.HandleMessages)

	discord.AddHandler(func(s *discordgo.Session, event *discordgo.Connect) {

		log.Println("Connected to discord!")

		s.UpdateStatusComplex(discordgo.UpdateStatusData{
			Activities: []*discordgo.Activity{
				{
					Name: "Dota 2",
					Type: discordgo.ActivityTypeGame,
				},
			},
		})

	})

	discord.AddHandler(func(s *discordgo.Session, event *discordgo.GuildCreate) {

		if event.Guild.Unavailable {
			return
		}

		var (
			guildModel = new(models.Guild)
			result     = db.Connection.Where("discord_id =?", event.ID).First(&guildModel)
		)

		if err := result.Error; err != nil {
			db.Connection.Create(&models.Guild{
				Name:               event.Name,
				DiscordID:          event.ID,
				UserID:             event.OwnerID,
				MainVoiceChannelID: "",
				MainTextChannelID:  "",
				Token:              components.Random(25),
			})
		}

	})

	// Open the websocket and begin listening.
	if err = discord.Open(); err != nil {
		log.Fatalf("Error opening Discord session: %v ", err)
	}

	log.Println("Pepe.bot is now running!")
}

func (a *Application) ListenAndServeGSIHttpServer(host string, port int) {

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		w.Header().Add("Content-Type", "application/json")

		var (
			response = new(components.GSIResponse)
			guild    = new(models.Guild)
		)

		if err := json.NewDecoder(r.Body).Decode(response); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    0,
				"status":  "failed",
				"message": "Could not decode body.",
			})
			return
		}

		if response.Provider.Appid != 570 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    0,
				"status":  "failed",
				"message": fmt.Sprintf("Provider should be {570/Dota2} Not [%d]", response.Provider.Appid),
			})
			return
		}

		if response.Map.Name != "start" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    0,
				"status":  "failed",
				"message": fmt.Sprintf("Map sould be start, not [%s]", response.Map.Name),
			})
			return
		}

		if response.Auth.Token == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    0,
				"status":  "failed",
				"message": "Token is required",
			})
			return
		}

		result := db.Connection.Where("token =?", response.Auth.Token).First(&guild)
		if err := result.Error; err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    0,
				"status":  "failed",
				"message": fmt.Sprintf("Could not find guild with token [%s]", response.Auth.Token),
			})
			return
		}

		discordGuild, err := a.Client.Guild(guild.DiscordID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    0,
				"status":  "failed",
				"message": fmt.Sprintf("Could not find discord guild with id [%s]", guild.DiscordID),
			})
			return
		}

		response.DiscordGuild = discordGuild
		response.Guild = guild

		a.GsiChannel <- response

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"code":   200,
			"status": "success",
		})
		return

	})

	log.Println(fmt.Sprintf("Dota 2 GSI Http server running! on %s:%d", host, port))

	// Listen and serve the gsi application
	log.Println(http.ListenAndServe(fmt.Sprintf("%s:%d", host, port), nil))
}

func (a *Application) CreateEmojiIfNotExists(GuildId string, emojiName, filename string) (emoji *discordgo.Emoji, err error) {
	img, err := imgbase64.FromLocal(filename)
	if err != nil {
		return
	}
	emoji, err = a.Client.GuildEmojiCreate(GuildId, emojiName, img, []string{})
	return
}
