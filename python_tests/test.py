from pyravendb.store import document_store
from pyravendb.raven_operations.server_operations import GetDatabaseNamesOperation, CreateDatabaseOperation, DeleteDatabaseOperation
from pyravendb.raven_operations.maintenance_operations import GetStatisticsOperation
from pyravendb.commands.raven_commands import GetTopologyCommand
import uuid

# def testLoad():
#     store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="PyRavenDB2")
#     store.initialize()

#     with store.open_session() as session:
#         foo = session.load("foos/1")
#         print(foo)

#     database_names = store.maintenance.server.send(GetDatabaseNamesOperation(0, 3))
#     print(database_names)

def testGetDatabaseNamesOp():
    store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="")
    store.initialize()
    op = GetDatabaseNamesOperation(0, 3)
    res = store.maintenance.server.send(op)
    print(res)

def testGetStatisticsOp():
    store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="PyRavenDB")
    store.initialize()
    op = GetStatisticsOperation()
    res = store.maintenance.send(op)
    print(res)

def testGetStatisticsBadDb():
    store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="not-exists")
    store.initialize()
    op = GetStatisticsOperation()
    res = store.maintenance.send(op)
    print(res)

def testGetTopology():
    store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="PyRavenDB")
    store.initialize()
    with store.open_session() as session:
        op = GetTopologyCommand()
        res = session.requests_executor.execute()
        print(res)

def testGetTopologyBadDb():
    store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="invalid-db")
    store.initialize()
    with store.open_session() as session:
        op = GetTopologyCommand()
        res = session.requests_executor.execute(op)
        print(res)

def testCreateDatabaseOp():
    store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="")
    store.initialize()
    op = CreateDatabaseOperation(database_name="TestDb")
    res = store.maintenance.server.send(op)
    print(res)

def testCreateAndDeleteDatabaseOp():
    randomName = uuid.uuid4().hex
    print("name: " + randomName)
    store =  document_store.DocumentStore(urls=["http://localhost:9999"], database="")
    store.initialize()
    op = CreateDatabaseOperation(database_name=randomName)
    res = store.maintenance.server.send(op)
    print(res)
    op = DeleteDatabaseOperation(database_name=randomName, hard_delete=False)
    res = store.maintenance.server.send(op)
    print(res)


def main():
    #testGetDatabaseNamesOp()
    #testGetStatisticsOp()
    #testGetStatisticsBadDb()
    #testGetTopology()
    #testGetTopology()
    #testGetTopologyBadDb()
    #testCreateDatabaseOp()
    testCreateAndDeleteDatabaseOp()

if __name__ == "__main__":
    main()

