package main

// Private chat (v0.19.1, Blaze Mobile's PrivateChat): the ghost button in the top bar opens a
// chat that is never written anywhere. No chat file, no long-chat index, no journal, no title,
// no learning, no memories or skills: the tools that write them are not offered and are refused
// if called anyway, and specialists (which keep files) aren't offered. Leaving throws it away.

import (
	"strings"

	"blaze-desktop/internal/llm"
)

const privatePrefix = "private-"

// isPrivate says whether a chat id is a private chat.
func isPrivate(id string) bool { return strings.HasPrefix(id, privatePrefix) }

// privateBlocked are the tools that would keep something from a private chat.
var privateBlocked = map[string]bool{
	"remember": true, "forget": true, "memory_save": true, "memory_update": true,
	"write_skill": true, "update_brief": true, "schedule": true,
}

// privateTools drops the tools a private chat may not use.
func privateTools(all []llm.Tool) []llm.Tool {
	out := make([]llm.Tool, 0, len(all))
	for _, t := range all {
		if !privateBlocked[t.Function.Name] {
			out = append(out, t)
		}
	}
	return out
}

const privateLine = "\nPRIVATE CHAT: nothing in this chat is saved or remembered. Don't save memories, skills or notes, and don't schedule anything; if asked, say it can't be done in a private chat.\n"

const privateRefusal = "Not in a private chat: nothing from it is saved. Leave the private chat to do this."

// OpenPrivate starts a new private chat and puts it on screen. Returns its id.
func (a *App) OpenPrivate() string {
	id := privatePrefix + newID()
	a.mu.Lock()
	a.cur, a.convID = &session{id: id, started: true}, id
	a.mu.Unlock()
	if a.conn != nil {
		a.conn.ResetLoaded()
	}
	return id
}

// LeavePrivate throws the private chat away (stopping its turn) and goes back to the main chat.
func (a *App) LeavePrivate() Conv {
	a.mu.Lock()
	for id, s := range a.live {
		if isPrivate(id) {
			if s.cancel != nil {
				s.cancel()
			}
			delete(a.live, id)
		}
	}
	if a.cur != nil && isPrivate(a.cur.id) {
		a.cur.hist = nil
	}
	a.mu.Unlock()
	return a.OpenMain()
}
