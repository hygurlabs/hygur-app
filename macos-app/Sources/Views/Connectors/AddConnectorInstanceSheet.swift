import SwiftUI

/// Sheet for adding a new instance of a multi-instance connector (e.g., a second IMAP account).
struct AddConnectorInstanceSheet: View {
    let typeID: String
    let typeName: String
    let onAdd: (String, String, [String: String]) -> Void

    @Environment(\.dismiss) private var dismiss

    @State private var instanceID = ""
    @State private var displayName = ""
    @State private var host = ""
    @State private var port = "993"
    @State private var username = ""
    @State private var password = ""
    @State private var tls = true
    @State private var idError: String?

    private var isValid: Bool {
        !instanceID.isEmpty && !displayName.isEmpty && !host.isEmpty && !username.isEmpty && !password.isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Add \(typeName) Account")
                        .font(.title3)
                        .fontWeight(.semibold)
                    Text("Configure a new \(typeName) account to index.")
                        .font(.callout)
                        .foregroundStyle(HygurColors.textSecondary)
                }
                Spacer()
                Button {
                    dismiss()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title3)
                        .foregroundStyle(HygurColors.textTertiary)
                }
                .buttonStyle(.plain)
            }
            .padding(HygurSpacing.lg)

            Divider()

            Form {
                Section("Account Identity") {
                    LabeledContent("Display name") {
                        TextField("e.g. Work, Personal", text: $displayName)
                            .textFieldStyle(.plain)
                    }
                    LabeledContent("Account ID") {
                        VStack(alignment: .leading, spacing: 4) {
                            TextField("e.g. imap_work", text: $instanceID)
                                .textFieldStyle(.plain)
                                .onChange(of: instanceID) { _, newValue in
                                    let valid = newValue.allSatisfy { $0.isLetter || $0.isNumber || $0 == "_" || $0 == "-" }
                                    idError = (!newValue.isEmpty && !valid) ? "Only letters, numbers, _ and - are allowed." : nil
                                    // Auto-sanitize spaces
                                    if newValue.contains(" ") {
                                        instanceID = newValue.replacingOccurrences(of: " ", with: "_")
                                    }
                                }
                            if let err = idError {
                                Text(err)
                                    .font(.caption)
                                    .foregroundStyle(.red)
                            }
                        }
                    }
                }

                Section("Server") {
                    LabeledContent("Host") {
                        TextField("imap.example.com", text: $host)
                            .textFieldStyle(.plain)
                    }
                    LabeledContent("Port") {
                        TextField("993", text: $port)
                            .textFieldStyle(.plain)
                    }
                    Toggle("Use TLS", isOn: $tls)
                }

                Section("Credentials") {
                    LabeledContent("Username") {
                        TextField("user@example.com", text: $username)
                            .textFieldStyle(.plain)
                    }
                    LabeledContent("Password / App password") {
                        SecureField("••••••••", text: $password)
                            .textFieldStyle(.plain)
                    }
                }
            }
            .formStyle(.grouped)

            Divider()

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Add Account") {
                    submit()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!isValid || idError != nil)
            }
            .padding(HygurSpacing.lg)
        }
        .frame(width: 480)
        .background(Color(nsColor: .windowBackgroundColor))
    }

    private func submit() {
        let settings: [String: String] = [
            "host": host,
            "port": port,
            "username": username,
            "password": password,
            "tls": tls ? "true" : "false"
        ]
        onAdd(instanceID, displayName, settings)
        dismiss()
    }
}
