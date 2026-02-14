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