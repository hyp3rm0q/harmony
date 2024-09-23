/* Copyright 2017 Vector Creations Ltd
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package synctypes

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/neilalexander/harmony/internal/gomatrixserverlib"
	"github.com/neilalexander/harmony/internal/gomatrixserverlib/spec"
)

type EventFieldsToVerify struct {
	EventID        string
	Type           string
	OriginServerTS spec.Timestamp
	StateKey       *string
	Content        spec.RawJSON
	Unsigned       spec.RawJSON
	Sender         string
	Depth          int64
	PrevEvents     []string
	AuthEvents     []string
}

func verifyEventFields(t *testing.T, got EventFieldsToVerify, want EventFieldsToVerify) {
	t.Helper()
	if got.EventID != want.EventID {
		t.Errorf("ClientEvent.EventID: wanted %s, got %s", want.EventID, got.EventID)
	}
	if got.OriginServerTS != want.OriginServerTS {
		t.Errorf("ClientEvent.OriginServerTS: wanted %d, got %d", want.OriginServerTS, got.OriginServerTS)
	}
	if got.StateKey == nil && want.StateKey != nil {
		t.Errorf("ClientEvent.StateKey: no state key present when one was wanted: %s", *want.StateKey)
	}
	if got.StateKey != nil && want.StateKey == nil {
		t.Errorf("ClientEvent.StateKey: state key present when one was not wanted: %s", *got.StateKey)
	}
	if got.StateKey != nil && want.StateKey != nil && *got.StateKey != *want.StateKey {
		t.Errorf("ClientEvent.StateKey: wanted %s, got %s", *want.StateKey, *got.StateKey)
	}
	if got.Type != want.Type {
		t.Errorf("ClientEvent.Type: wanted %s, got %s", want.Type, got.Type)
	}
	if !bytes.Equal(got.Content, want.Content) {
		t.Errorf("ClientEvent.Content: wanted %s, got %s", string(want.Content), string(got.Content))
	}
	if !bytes.Equal(got.Unsigned, want.Unsigned) {
		t.Errorf("ClientEvent.Unsigned: wanted %s, got %s", string(want.Unsigned), string(got.Unsigned))
	}
	if got.Sender != want.Sender {
		t.Errorf("ClientEvent.Sender: wanted %s, got %s", want.Sender, got.Sender)
	}
	if got.Depth != want.Depth {
		t.Errorf("ClientEvent.Depth: wanted %d, got %d", want.Depth, got.Depth)
	}
	if !reflect.DeepEqual(got.PrevEvents, want.PrevEvents) {
		t.Errorf("ClientEvent.PrevEvents: wanted %v, got %v", want.PrevEvents, got.PrevEvents)
	}
	if !reflect.DeepEqual(got.AuthEvents, want.AuthEvents) {
		t.Errorf("ClientEvent.AuthEvents: wanted %v, got %v", want.AuthEvents, got.AuthEvents)
	}
}

func TestToClientEvent(t *testing.T) { // nolint: gocyclo
	ev, err := gomatrixserverlib.MustGetRoomVersion(gomatrixserverlib.RoomVersionV1).NewEventFromTrustedJSON([]byte(`{
		"type": "m.room.name",
		"state_key": "",
		"event_id": "$test:localhost",
		"room_id": "!test:localhost",
		"sender": "@test:localhost",
		"content": {
			"name": "Hello World"
		},
		"origin_server_ts": 123456,
		"unsigned": {
			"prev_content": {
				"name": "Goodbye World"
			}
		}
	}`), false)
	if err != nil {
		t.Fatalf("failed to create Event: %s", err)
	}
	userID, err := spec.NewUserID("@test:localhost", true)
	if err != nil {
		t.Fatalf("failed to create userID: %s", err)
	}
	sk := ""
	ce := ToClientEvent(ev, FormatAll)

	verifyEventFields(t,
		EventFieldsToVerify{
			EventID:        ce.EventID,
			Type:           ce.Type,
			OriginServerTS: ce.OriginServerTS,
			StateKey:       ce.StateKey,
			Content:        ce.Content,
			Unsigned:       ce.Unsigned,
			Sender:         ce.Sender,
		},
		EventFieldsToVerify{
			EventID:        ev.EventID(),
			Type:           ev.Type(),
			OriginServerTS: ev.OriginServerTS(),
			StateKey:       &sk,
			Content:        ev.Content(),
			Unsigned:       ev.Unsigned(),
			Sender:         userID.String(),
		})

	j, err := json.Marshal(ce)
	if err != nil {
		t.Fatalf("failed to Marshal ClientEvent: %s", err)
	}
	// Marshal sorts keys in structs by the order they are defined in the struct, which is alphabetical
	out := `{"content":{"name":"Hello World"},"event_id":"$test:localhost","origin_server_ts":123456,` +
		`"room_id":"!test:localhost","sender":"@test:localhost","state_key":"","type":"m.room.name",` +
		`"unsigned":{"prev_content":{"name":"Goodbye World"}}}`
	if !bytes.Equal([]byte(out), j) {
		t.Errorf("ClientEvent marshalled to wrong bytes: wanted %s, got %s", out, string(j))
	}
}

func TestToClientFormatSync(t *testing.T) {
	ev, err := gomatrixserverlib.MustGetRoomVersion(gomatrixserverlib.RoomVersionV1).NewEventFromTrustedJSON([]byte(`{
		"type": "m.room.name",
		"state_key": "",
		"event_id": "$test:localhost",
		"room_id": "!test:localhost",
		"sender": "@test:localhost",
		"content": {
			"name": "Hello World"
		},
		"origin_server_ts": 123456,
		"unsigned": {
			"prev_content": {
				"name": "Goodbye World"
			}
		}
	}`), false)
	if err != nil {
		t.Fatalf("failed to create Event: %s", err)
	}
	ce := ToClientEvent(ev, FormatSync)
	if ce.RoomID != "" {
		t.Errorf("ClientEvent.RoomID: wanted '', got %s", ce.RoomID)
	}
}

func TestToClientEventFormatSyncFederation(t *testing.T) { // nolint: gocyclo
	ev, err := gomatrixserverlib.MustGetRoomVersion(gomatrixserverlib.RoomVersionV10).NewEventFromTrustedJSON([]byte(`{
		"type": "m.room.name",
		"state_key": "",
		"event_id": "$test:localhost",
		"room_id": "!test:localhost",
		"sender": "@test:localhost",
		"content": {
			"name": "Hello World"
		},
		"origin_server_ts": 123456,
		"unsigned": {
			"prev_content": {
				"name": "Goodbye World"
			}
		},
        "depth": 8,
        "prev_events": [
          "$f597Tp0Mm1PPxEgiprzJc2cZAjVhxCxACOGuwJb33Oo"
        ],
        "auth_events": [
          "$Bj0ZGgX6VTqAQdqKH4ZG3l6rlbxY3rZlC5D3MeuK1OQ",
          "$QsMs6A1PUVUhgSvmHBfpqEYJPgv4DXt96r8P2AK7iXQ",
          "$tBteKtlnFiwlmPJsv0wkKTMEuUVWpQH89H7Xskxve1Q"
        ]
	}`), false)
	if err != nil {
		t.Fatalf("failed to create Event: %s", err)
	}
	userID, err := spec.NewUserID("@test:localhost", true)
	if err != nil {
		t.Fatalf("failed to create userID: %s", err)
	}
	sk := ""
	ce := ToClientEvent(ev, FormatSyncFederation)

	verifyEventFields(t,
		EventFieldsToVerify{
			EventID:        ce.EventID,
			Type:           ce.Type,
			OriginServerTS: ce.OriginServerTS,
			StateKey:       ce.StateKey,
			Content:        ce.Content,
			Unsigned:       ce.Unsigned,
			Sender:         ce.Sender,
			Depth:          ce.Depth,
			PrevEvents:     ce.PrevEvents,
			AuthEvents:     ce.AuthEvents,
		},
		EventFieldsToVerify{
			EventID:        ev.EventID(),
			Type:           ev.Type(),
			OriginServerTS: ev.OriginServerTS(),
			StateKey:       &sk,
			Content:        ev.Content(),
			Unsigned:       ev.Unsigned(),
			Sender:         userID.String(),
			Depth:          ev.Depth(),
			PrevEvents:     ev.PrevEventIDs(),
			AuthEvents:     ev.AuthEventIDs(),
		})
}
