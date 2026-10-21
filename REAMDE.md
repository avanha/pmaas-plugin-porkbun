# porkbun Plugin

Plugin to work with the porkbun.com API, for retireving and updating DNS records.

### TODO

1.  ~~Done - Hook up the retry queue logic.~~
    1. Retry requests on network errors.
1.  In Progress - Implement a status web page.
1.  ~~Done - Move the RequestQueue implementation into a common package.~~
1.  Move the ThreadSafeEntityWrapper into a common SPI package.
    1.  Add a version that doesn't use `EnqueueOnPluginGoRoutine` for use by entities
        that take of thread-safety on their own.

### Checking that a record is what it should be

Porkbun saying an edit worked is not the same as the record being changed, so the plugin checks.

- **After an update** it asks Porkbun for the record again. If Porkbun still has the old value the update is an error, which the retry
  queue tries again, and when it gives up the record's alert says the value could not be saved. Whether an update is needed is
  always decided from a fresh read, never from one kept from earlier.
- **A read** (every four hours, and after every update) that has a value other than the one last asked for, with no update on its
  way, is the same alert.
- **The resolver** of the machine the plugin runs on is asked for each record of type A, AAAA, CNAME or TXT after each read, and again
  every five minutes for as long as its answer differs from the record's value. Until the record's TTL and five minutes have passed since
  the record was changed (or, for one that was not changed by this plugin, since the answer was first seen to differ) a different answer is
  not a problem: a cache is entitled to it. After that it is, and the alert says what resolved.
- **One alert per record** is raised, as a warning, from the source `porkbun` with the key `domain/type/name`. It says what is wrong,
  and clears when nothing is.
- **Check and update now**, the button at the top of the page, checks every record again, and sends the value last asked for again
  (even if Porkbun says it already has it) to any record for which Porkbun, or the resolver, has another value. A forced update clears
  the alert and starts the TTL and grace over, so the alert comes back only if the resolver still differs after that. The value last asked
  for is kept in memory: after the plugin has restarted, a record has none until the assembly asks for one.

### Notes

- Escape Analysis: `go build -gcflags="-m -m" . &> ea.txt`
- Uses a different (and hopefully better) entity stub approach: common.ThreadSafeEntityWrapper.
  The wrapper holds an atomic reference to the container and target entity, and uses those to enqueue function 
  calls to the entity via the `spi.IPMAASContainer.EnqueueOnPluginGoRoutine` method.  The stub's references to the
  container and target entity can be cleared when the entity is deleted which will allows the entity to be garbage
  collected.
- Supports method invocations on entities via the config interface.  This allows the following code in an assembly: \
  ##### During configuration: 

  ```go
  conf := porkbunconfig.NewPluginConfig()
  conf.ApiKey = "porkbunApiKey"
  conf.ApiSecret = "porkbunApiSecret"
  exampledotcom := conf.AddDomain("example.com")
  wwwDnsRecord := exampledotcom.AddDnsRecord("A", "www")
  ```
  
  ##### Later, in an event handler:

  ```go
  func onPublicIpChanged(event events.HostInterfaceAddressChangeEvent) {
    avanhaDnsRecord.UpdateValue(event.NewValue[0])
  }
  ```
  
  This is built on a config-time `DnsRecordEntityStubAvailableEvent` which fires to listeners after the entity is
  registered with the server.  The config entity instance receives the event via a callback on the Goroutine that
  called `PMAAS.Run()` and saves the entity stub, so it can pass through invocations to it.  The config entity does
  not currently implement the entity interface, though it probably could.