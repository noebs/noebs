# Notes on go
- First write the HTTP Client library for EBS.
- Fields validation issue.
- The good thing in w http.ResponseWriter is that i can, at anytime, just raise 400s! I mean, WOW!
- router handler, such that you'd transfer your request into it.
- multiple response.WriteHeader calls. I totally got that. Now fixing.
- store the incoming request into a EndpointFields handler so you can decode/encode it multiple times. Good 
in case you need to do logging/further validation with the request body.
