// Package journeys holds Facet's journey logic for the Facet App, ready to be
// mounted by whatever host serves it:
//
//   - listing and inspecting production projects from the evidence on disk
//     (ListProjects, GetProjectDetails), and locating one by slug
//     (LocateProject);
//   - the app-owned project catalog: list, create, open and save
//     (LoadCatalog, CreateProject, OpenProject, RegisterProject, SaveCatalog);
//   - importing the person's media into a project's assets/ folder
//     (ImportMedia, SafeMediaName);
//   - technical review of a project render through the toolbox's
//     output_review tool (ReviewRender);
//   - the human review decision, recorded as a project file against the
//     reviewed file's SHA-256 digest (RecordDecision), and reported with the
//     project's details together with whether the file still has that digest;
//   - safe resolution of project media for serving (ResolveMedia,
//     ResolveMediaRef).
//
// The package has no transport, no sessions and no model calls. It never
// serves anything: a host maps its own requests onto these functions and maps
// the sentinel errors (ErrInvalidRequest, ErrNotFound, ErrOutsideProject,
// ErrUnsupportedMedia, ErrConflict, ErrTooLarge) onto its own responses.
//
// Every location is explicit. Callers pass an Options value naming the
// workspace, the catalog file and the default productions root; the package
// never consults the environment, the user's home directory or OS
// application-data folders to find them.
//
// Evidence locations are reported as media references: "projects/<slug>/<file>"
// for a project inside the workspace and "catalog/<id>/<file>" for a catalog
// project. ResolveMediaRef maps a reference back to the one file it names, and
// Options.MediaURLPrefix, when set, also reports each reference as a URL under
// the host's media route.
//
// Project files are optional production records, not required stages: the
// scanners report evidence that exists and invent none. Opening an existing
// folder writes nothing into it, and creating a project creates only its
// directory and an app-owned catalog entry. Importing writes only the
// imported file, inside the project's assets/ folder. Successful technical
// review is evidence, not acceptance; only RecordDecision records a human
// decision.
package journeys
