The package strategy that I use for my projects involves 4 simple tenets:

Root package is for domain types
Group subpackages by dependency
Use a shared mock subpackage
Main package ties together dependencies
These rules help isolate our packages and define a clear domain language across the entire application. Let’s look at how each one of these rules works in practice.

#1. Root package is for domain types
Your application has a logical, high-level language that describes how data and processes interact. This is your domain. If you have an e-commerce application your domain involves things like customers, accounts, charging credit cards, and handling inventory. If you’re Facebook then your domain is users, likes, & relationships. It’s the stuff that doesn’t depend on your underlying technology.

I place my domain types in my root package. This package only contains simple data types like a User struct for holding user data or a UserService interface for fetching or saving user data.

It may look something like:


This makes your root package extremely simple. You may also include types that perform actions but only if they solely depend on other domain types. For example, you could have a type that polls your UserService periodically. However, it should not call out to external services or save to a database. That is an implementation detail.

The root package should not depend on any other package in your application!

#2. Group subpackages by dependency
If your root package is not allowed to have external dependencies then we must push those dependencies to subpackages. In this approach to package layout, subpackages exist as an adapter between your domain and your implementation.

For example, your UserService might be backed by PostgreSQL. You can introduce a postgres subpackage in your application that provides a postgres.UserService implementation:


This isolates our PostgreSQL dependency which simplifies testing and provides an easy way to migrate to another database in the future. It can be used as a pluggable architecture if you decide to support other database implementations such as BoltDB.

Get Ben Johnson’s stories in your inbox
Join Medium for free to get updates from this writer.

Enter your email
Subscribe

Remember me for faster sign in

It also gives you a way to layer implementations. Perhaps you want to hold an in-memory, LRU cache in front of PostgreSQL. You can add a UserCache that implements UserService which can wrap your PostgreSQL implementation:


We see this approach in the standard library too. The io.Reader is a domain type for reading bytes and its implementations are grouped by dependency — tar.Reader, gzip.Reader, multipart.Reader. These can be layered as well. It’s common to see an os.File wrapped by a bufio.Reader which is wrapped by a gzip.Reader which is wrapped by a tar.Reader.

Dependencies between dependencies
Your dependencies don’t live in isolation. You may store User data in PostgreSQL but your financial transaction data exists in a third party service like Stripe. In this case we wrap our Stripe dependency with a logical domain type — let’s call it TransactionService.

By adding our TransactionService to our UserService we decouple our two dependencies:

type UserService struct {
        DB *sql.DB
        TransactionService myapp.TransactionService
}
Now our dependencies communicate solely through our common domain language. This means that we could swap out PostgreSQL for MySQL or switch Stripe for another payment processor without affecting other dependencies.

Don’t limit this to third party dependencies
This may sound odd but I also isolate my standard library dependencies with this same method. For instance, the net/http package is just another dependency. We can isolate it as well by including an http subpackage in our application.

It might seem odd to have a package with the same name as the dependency it wraps, however, this is intentional. There are no package name conflicts in your application unless you allow net/http to be used in other parts of your application. The benefit to duplicating the name is that it requires you to isolate all HTTP code to your http package.


Now your http.Handler acts as an adapter between your domain and the HTTP protocol.

#3. Use a shared mock subpackage
Because our dependencies are isolated from other dependencies by our domain interfaces, we can use these connection points to inject mock implementations.

There are several mocking libraries such as GoMock that will generate mocks for you but I personally prefer to just write them myself. I find many of the mocking tools to be overly complicated.

The mocks I use are very simple. For example, a mock for the UserService looks like:


This mock lets me inject functions into anything that uses the myapp.UserService interface to validate arguments, return expected data, or inject failures.

Let’s say we want to test our http.Handler that we built above:


Our mock lets us completely isolate our unit test to only the handling of the HTTP protocol.

#4. Main package ties together dependencies
With all these dependency packages floating around in isolation, you may wonder how they all come together. That’s the job of the main package.

Main package layout
An application may produce multiple binaries so we’ll use the Go convention of placing our main package as a subdirectory of the cmd package. For example, our project may have a myapp server binary but also a myappctl client binary for managing the server from the terminal. We’ll layout our main packages like this:

myapp/
    cmd/
        myapp/
            main.go
        myappctl/
            main.go
Injecting dependencies at compile time
The term “dependency injection” has gotten a bad rap. It conjures up thoughts of verbose Spring XML files. However, all the term really means is that we’re going to pass dependencies to our objects instead of requiring that the object build or find the dependency itself.

The main package is what gets to choose which dependencies to inject into which objects. Because the main package simply wires up the pieces, it tends to be fairly small and trivial code:


It’s also important to note that your main package is also an adapter. It connects the terminal to your domain.

Conclusion
Application design is a hard problem. There are so many design decisions to make and without a set of solid principles to guide you the problem is made even worse. We’ve looked at several current approaches to Go application design and we’ve seen many of their flaws.

I believe approaching design from the standpoint of dependencies makes code organization simpler and easier to reason about. First we design our domain language. Then we isolate our dependencies. Next we introduce mocks to isolate our tests. Finally, we tie everything together within our main package.
