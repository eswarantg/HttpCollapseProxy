# HttpCollapseProxy
Proxy that collapses request sent to Proxy Server.

# MultiTeeReaderWithFullRead
Used by HttpCollapseProxy
Builds upon TeeReader concept, with the following extensions
* Accepts multiple Writers
* Allows for adding Writers after construction
* Upon mid-Close, reads till EOF and copies over to the Writers
