package ravendb

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func compactTest_canCompactDatabase(t *testing.T) {
	var err error
	store := getDocumentStoreMust(t)
	defer store.Close()

	{
		newSession := openSessionMust(t, store)
		user1 := NewUser()
		user1.setLastName("user1")
		err = newSession.StoreEntityWithID(user1, "users/1")
		assert.NoError(t, err)
		err = newSession.SaveChanges()
		assert.NoError(t, err)
		newSession.Close()
	}

	compactSettings := NewCompactSettings()
	compactSettings.setDatabaseName(store.getDatabase())
	compactSettings.setDocuments(true)

	compactOp := NewCompactDatabaseOperation(compactSettings)
	operation, err := store.maintenance().server().sendAsync(compactOp)
	assert.NoError(t, err)

	// we can't compact in memory database but here we just test is request was send successfully
	err = operation.waitForCompletion()
	msg := err.Error()
	assert.True(t, strings.Contains(msg, "Unable to cast object of type 'PureMemoryStorageEnvironmentOptions' to type 'DirectoryStorageEnvironmentOptions'"))
}

func TestCompact(t *testing.T) {
	if dbTestsDisabled() {
		return
	}

	destroyDriver := createTestDriver(t)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered in %s\n", t.Name())
		}
		destroyDriver()
	}()

	// follows execution order of java tests
	compactTest_canCompactDatabase(t)
}
