import * as cp from 'child_process'
import * as fs from 'fs'
import * as net from 'net'
import * as os from 'os'
import * as path from 'path'
import * as util from 'util'
import * as vscode from 'vscode'
import * as lc from 'vscode-languageclient/node'
import * as lockfile from 'proper-lockfile'
import {Tail} from 'tail'
import YAML from 'yaml'
import * as semver from 'semver'
import * as utils from './utils'

const readFileAsync = util.promisify(fs.readFile)
const statAsync = util.promisify(fs.stat)
const execAsync = util.promisify(cp.exec)

const UBER_ULSP_CONFIG = 'uber.uberLanguageServer'
const ULSP_ENABLED = 'enablementStatus'
const LAUNCH_LOCKFILE = path.join(os.tmpdir(), 'ulsplaunch.lock')
const LAUNCH_LOCKFILE_TIMEOUT = 5000
const CUSTOM_OUTPUT_MAX_LINES = 5000

enum LSP_ENABLED_CHOICES {
  enabled = 'Enabled',
  auto = 'Auto',
  disabled = 'Disabled',
}
const SERVER_BINARY_PATH = 'serverBinaryPath'
const SERVER_BINARY_PATH_DEFAULT = '/usr/local/bin/scip-lsp'
const SERVER_CONFIG_DIRECTORY = 'serverConfigDirectory'
const SERVER_CONFIG_DIRECTORY_DEFAULT = '/usr/local/bin/scip-lsp/config'
const SERVER_INFO_FILE = 'serverInfoFile'
const SERVER_DEVELOPMENT_MODE = 'serverDevelopmentMode'
const LANGUAGE_CLIENT_OPTIONS = 'languageClientOptions'

const outputChannelClient = vscode.window.createOutputChannel('SCIP LSP Client')
const outputChannelServerActions = vscode.window.createOutputChannel(
  'SCIP LSP Server Actions'
)

// ServerLaunchConfig stores paths and other info used to launch the LSP server.
export interface ServerLaunchConfig {
  cmd: string
  configDir: string
  infoFile: string
  serverReadyLine: string
  devMode: boolean
}

interface connectionInfo {
  port: number
  host: string
}

// Definition for the object stored in the languageClientOptions setting.
type languageClientSettings = {
  documentSelector?: lc.DocumentSelector
  fileWatcherPatterns?: string[]
}

const defaultServerLaunchConfig = {
  infoFile: path.join(os.homedir(), '.ulspd'),
  serverReadyLine: 'started JSON-RPC inbound',
}

interface customOutputChannel {
  id: string
  channel: vscode.OutputChannel
  tail?: Tail
}

export async function getUlspEnablementStatus(): Promise<boolean> {
  const ulspConfig = vscode.workspace.getConfiguration(UBER_ULSP_CONFIG)
  const ulspEnabledSetting = ulspConfig.get<string>(ULSP_ENABLED)
  return (
    ulspEnabledSetting === LSP_ENABLED_CHOICES.enabled ||
    ulspEnabledSetting === LSP_ENABLED_CHOICES.auto
  )
}
/**
 * LSP client will handle connections to uLSP.
 * */
export class LSPClient {
  private conn?: connectionInfo
  private serverLaunchConfig?: ServerLaunchConfig
  private outputChannelServerInit?: vscode.OutputChannel
  private outputChannelServerActions?: vscode.OutputChannel
  private customOutputChannels: customOutputChannel[] = []
  private outputChannelClient?: vscode.OutputChannel
  private client?: lc.LanguageClient
  private restartCount = 0

  public async activate(context: vscode.ExtensionContext): Promise<void> {
    try {
      await vscode.commands.executeCommand('setContext', 'ulspEnable', true)
      context.subscriptions.push(
        vscode.commands.registerCommand(
          'uber.lsp.restart',
          this.restart.bind(this)
        )
      )

      context.subscriptions.push(
        vscode.commands.registerCommand(
          'uber.lsp.showCustomOutputChannel',
          this.showCustomOutput.bind(this)
        )
      )
      await this.start()
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      void utils.sendTelemetryEvent('ulsp_activation_error', {
        message,
      })
      outputChannelClient.appendLine(`Activation error: ${err}`)
      const selection = 'Show Output'
      vscode.window
        .showErrorMessage(
          'Uber Language Server client failed to activate. Some language features may not be available. Check the output panel for more information.',
          selection
        )
        .then(data => {
          if (data === selection) outputChannelClient.show()
        })
    }

    void utils.sendTelemetryEvent('ulsp_activation_success', {})
  }

  public async start(): Promise<void> {
    this.outputChannelClient = outputChannelClient
    this.outputChannelServerActions = outputChannelServerActions
    try {
      this.serverLaunchConfig = await this.getLSPServerLaunchConfig()
    } catch {
      void vscode.window.showErrorMessage(
        'LSP client initialization failed due to missing setting. Please check the uLSP Server Binary Path and Server Config Directory settings.'
      )
      return
    }

    const isRunning = await this.ensureRunningServer()
    if (!isRunning) return

    if (this.client !== undefined) {
      await this.client.dispose()
    }

    this.client = new lc.LanguageClient(
      'uber-lsp',
      'Uber Language Server',
      async () => await this.startStream(),
      this.getLanguageClientOptions()
    )

    this.client.onTelemetry(event => {
      this.outputChannelClient?.appendLine(`Telemetry event: ${event as string}`)
    })

    this.client.onNotification('window/logMessage', message =>
      this.customHandlerLogMessage(message)
    )

    await this.client.start()
    await this.setupCustomOutputChannels()
  }

  private async restart(): Promise<void> {
    let restartULSPServer = false

    try {
      if (this.client !== undefined && !this.serverLaunchConfig?.devMode) {
        restartULSPServer = await this.quickPickShouldRestartULSP()

        void utils.sendTelemetryEvent('ulsp_manual_restart_requested', {
          restart_ulsp: restartULSPServer.toString(),
        })

        if (restartULSPServer) {
          this.outputChannelClient?.appendLine(
            'INFO: Requesting full server shutdown.'
          )
          await this.client.sendRequest('ulsp/requestFullShutdown')
        }

        this.outputChannelClient?.appendLine('INFO: Stopping client.')
        await this.client.stop()

        // Give server time to shutdown before restarting.
        await new Promise(resolve => setTimeout(resolve, 5000))
      }
      await this.start()
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : String(err)
      this.outputChannelClient?.appendLine(errMsg)
    }
  }

  /**
   * Ensure that the uLSP daemon is running and available, by checking for local connection info or starting a new server if necessary.
   * @returns Promise indicating the availability of a running server that can accept connections.
   */
  private async ensureRunningServer(): Promise<boolean> {
    const existingAddress = await this.getExistingServerAddress()
    const existingServerAvailable =
      await this.checkExistingServer(existingAddress)

    if (existingServerAvailable) {
      this.conn = existingAddress as connectionInfo
      return true
    } else if (this.serverLaunchConfig?.devMode) {
      // In dev mode, do not attempt to start a new server.
      this.outputChannelClient?.appendLine(
        'INFO: No server found. Server Dev Mode is enabled, so a new server will not be started.'
      )
      return false
    }

    const newServerInfo = await this.initServer()
    if (newServerInfo === undefined) {
      void vscode.window.showErrorMessage(
        'LSP client initialization failed due to error starting server.'
      )
      return false
    }
    this.conn = newServerInfo
    return true
  }

  /**
   * Initalize a new uLSP daemon process, to be used if not already available.
   * @returns connectionInfo to the server, or undefined if unable to be determined.
   */
  private async initServer(): Promise<connectionInfo | undefined> {
    let releaseLock: any
    try {
      // Use a lockfile to coordianate between multiple VS Code windows.
      // Only the first process to acquire the lock needs to start the server.
      releaseLock = await lockfile.lock(LAUNCH_LOCKFILE, {
        realpath: false,
        stale: LAUNCH_LOCKFILE_TIMEOUT,
      })
    } catch (err) {
      if ((err as {code: string}).code != 'ELOCKED') {
        throw err
      }

      // If the lockfile is already held, wait for the other process to finish starting the server.
      this.outputChannelClient?.appendLine(
        'INFO: Launch detected via another VS Code window. Pausing to allow it to complete.'
      )

      await new Promise(resolve => setTimeout(resolve, LAUNCH_LOCKFILE_TIMEOUT))
      return await this.getExistingServerAddress()
    }

    if (this.outputChannelServerInit === undefined)
      this.outputChannelServerInit = vscode.window.createOutputChannel(
        'Uber LSP Server Initialization'
      )
    else this.outputChannelServerInit.appendLine('\n\n==== NEW SERVER ====')
    this.outputChannelClient?.appendLine(
      'INFO: Initializing a new server process. See output in "Uber LSP Server Initialization" channel.'
    )

    // Bazel depends on the parent process environment, as well as additional ULSP_CONFIG_DIR needed to launch service.
    const currentEnv = {...process.env}
    currentEnv.ULSP_CONFIG_DIR = this.serverLaunchConfig?.configDir

    // Process will be detached, so write stdout and stderr to a temporary file.
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'ulsp-'))
    const tmpFileName = path.join(tmpDir, 'log.txt')
    this.outputChannelServerInit.appendLine(
      `Server output file: ${tmpFileName}`
    )
    await execAsync(`touch ${tmpFileName}`)

    // Support both MacOS and Linux
    const fullCmd =
      process.platform === 'darwin'
        ? `nohup ${this.serverLaunchConfig?.cmd} >${tmpFileName} 2>&1 &`
        : `setsid ${this.serverLaunchConfig?.cmd} >${tmpFileName} 2>&1 &`

    // Wait for the server to indicate readiness.
    // Reject if server does not indicate readiness within timeout.
    await new Promise<void>((resolve, reject) => {
      setTimeout(async () => {
        await releaseLock()
        reject(
          Error(
            'Language server start exceeded timeout, see "Uber LSP Server Initialization" output panel for details'
          )
        )
      }, 30000)

      const tail = new Tail(tmpFileName)
      tail.on('line', async (line: string) => {
        this.outputChannelServerInit?.appendLine(line)
        if (this.serverLaunchConfig && line.includes(this.serverLaunchConfig.serverReadyLine)) {
          await releaseLock()
          resolve()
        }
      })

      const serverProcess = cp.spawn(fullCmd, {
        env: currentEnv,
        detached: true,
        shell: true,
        stdio: 'ignore',
      })

      serverProcess.unref()
    })

    // After server has been started, check its output file for connection info.
    return await this.getExistingServerAddress()
  }

  /**
   * Start a new socket connection using the currently set connection info.
   * @returns StreamInfo of the newly created connection.
   */
  private async startStream(): Promise<lc.StreamInfo> {
    if (this.conn === undefined) throw Error('Server connection info not set.')

    this.outputChannelClient?.appendLine(
      `INFO: Connecting to server at ${this.conn.host}:${this.conn.port}`
    )
    const socket = net.connect(this.conn)
    const result: lc.StreamInfo = {
      writer: socket,
      reader: socket,
    }

    return await Promise.resolve(result)
  }

  /**
   * Check for the presence of a .ulspd file and extract available connection info.
   * @returns connectionInfo based on the address contained in the file, or undefined if .ulspd file does not exist.
   */
  private async getExistingServerAddress(): Promise<
    connectionInfo | undefined
  > {
    try {
      // Example format: {"lsp-address":":5859"} or {"lsp-address":"1.2.3.4:5859"}
      const jsonResult = await this.readServerInfoFile()
      let entry = jsonResult['lsp-address']
      let [host, port] = entry?.split(':') ?? [undefined, NaN]

      if (isNaN(port)) return undefined
      if (host === '') host = 'localhost'

      return {
        host,
        port: parseInt(port),
      }
    } catch (err) {
      return undefined
    }
  }

  /**
   * Read the server info file from the configured location and return results.
   * @returns Promise containing the contents of the .ulspd file, parsed as JSON.
   */
  private async readServerInfoFile(): Promise<any> {
    if (!this.serverLaunchConfig) {
      throw new Error(
        'Server launch configuration is not set. Cannot read server info file.'
      )
    }
    await statAsync(this.serverLaunchConfig.infoFile)
    const data = await readFileAsync(this.serverLaunchConfig.infoFile, 'utf-8')
    this.outputChannelClient?.appendLine(
      `INFO: Server information available in ${this.serverLaunchConfig.infoFile}`
    )

    return JSON.parse(data)
  }

  /**
   * Set up output channels to watch for any additional output files as specified by the server output file.
   * This will check for any entries with keys in the format of "output:sample" and tail the corresponding file.
   * @returns Promise indicating completion of setup.
   * */
  private async setupCustomOutputChannels(): Promise<void> {
    // Clean up any existing output channels and tailed files.
    this.cleanupCustomOutput()

    const infoFileJson: {string: string} = await this.readServerInfoFile()
    for (const [key, outputFilePath] of Object.entries(infoFileJson)) {
      // Only watch files that are specified in the info file and have a key in the format of "output:sample".
      if (!key.startsWith('output:') || outputFilePath == undefined) {
        continue
      }

      // Extract the display name from the key and set up the channel.
      const channelID = key?.split(':')[1] ?? key
      const outputChannelName = `Uber LSP Output: ${channelID}`
      const channel = vscode.window.createOutputChannel(outputChannelName)
      channel.appendLine(`INFO: Output from file: ${outputFilePath}`)
      const currentEntry: customOutputChannel = {
        channel: channel,
        id: channelID,
      }
      this.customOutputChannels.push(currentEntry)

      this.outputChannelClient?.appendLine(
        `INFO: Added ${channelID} channel to watch output from ${outputFilePath}.`
      )

      // Begin tailing the file, and append any new lines to the output channel.
      // For memory usage and ease of navigation, limit the number of lines shown in the output panel to CUSTOM_OUTPUT_MAX_LINES.
      try {
        currentEntry.tail = new Tail(outputFilePath)
        let lines = 0
        currentEntry.tail.on('line', async (line: string) => {
          if (lines > CUSTOM_OUTPUT_MAX_LINES) {
            channel.clear()
            channel.appendLine(
              `INFO: Showing most recent lines. See prior output in: ${outputFilePath}`
            )
            lines = 0
          }
          channel.appendLine(line)
          lines++
        })

        // Stop watching on error or deleted file.
        // The output channel will remain available until the next time the client is restarted.
        currentEntry.tail.on('error', async (err: Error) => {
          channel.appendLine(`ERROR: ${err.message}`)
          currentEntry.tail!.unwatch()
        })
      } catch (err) {
        const errMsg = err instanceof Error ? err.message : String(err)
        currentEntry.channel.appendLine(
          `ERROR: Unable to display output in this channel. ${errMsg}`
        )
      }
    }
    return
  }

  /**
   * Clean up any existing output channels and tailed files.
   */
  private cleanupCustomOutput(): void {
    for (const channel of this.customOutputChannels ?? []) {
      channel.channel.dispose()
      channel.tail!.unwatch()
    }
    this.customOutputChannels = []
  }

  private showCustomOutput(channelId: string) {
    if (!channelId) {
      vscode.window.showErrorMessage(
        'No channel ID was included output channel display request.'
      )
      return
    }

    let outputChannel = this.customOutputChannels.find(c => c.id === channelId)
    if (!outputChannel) {
      vscode.window.showErrorMessage(
        `No output channel found with ID: ${channelId}`
      )
      return
    }
    outputChannel.channel.show()
  }

  /**
   * Confirm that there is an available running uLSP server using the  given connection info.
   * @param conn connectionInfo which will be checked for an available running server.
   * @returns boolean indicating whether or not to proceed using the given connection info.
   */
  private async checkExistingServer(
    conn: connectionInfo | undefined
  ): Promise<boolean> {
    if (conn === undefined) return false

    return await new Promise(resolve => {
      // LSP protocol does not directly support a health check, and initialize has side effects if sent more than once.
      // In order to check that the service is running, confirm that it can accept a connection.
      const testConnection = net.createConnection(conn.port, conn.host, () => {
        testConnection.end()
        this.outputChannelClient?.appendLine(
          `INFO: Existing server is available on port ${conn.port}`
        )
        resolve(true)
      })

      testConnection.on('error', () => {
        testConnection.end()
        this.outputChannelClient?.appendLine(
          'INFO: No existing server available'
        )
        resolve(false)
      })
    })
  }

  protected async getLSPServerLaunchConfig(): Promise<ServerLaunchConfig> {
    const ulspConfig = vscode.workspace.getConfiguration(UBER_ULSP_CONFIG)

    let cmd = ulspConfig.get<string>(SERVER_BINARY_PATH)
    let configDir = ulspConfig.get<string>(SERVER_CONFIG_DIRECTORY)
    const infoFile = ulspConfig.get<string>(SERVER_INFO_FILE)
    const devMode = ulspConfig.get<boolean>(SERVER_DEVELOPMENT_MODE)

    const configVersion = await this.getLSPServerVersion(cmd)
    const defaultVersion = await this.getLSPServerVersion(
      SERVER_BINARY_PATH_DEFAULT
    )
    if (
      !cmd ||
      !configDir ||
      this.compareLSPServerVersions(configVersion, defaultVersion) < 0
    ) {
      cmd = SERVER_BINARY_PATH_DEFAULT
      configDir = SERVER_CONFIG_DIRECTORY_DEFAULT
    }

    return {
      // Values that are configurable based on settings.
      cmd: cmd,
      configDir: configDir,
      devMode: devMode ?? false,

      // Values that always use the default value.
      infoFile: infoFile ?? defaultServerLaunchConfig.infoFile,
      serverReadyLine: defaultServerLaunchConfig.serverReadyLine,
    }
  }

  private async getLSPServerVersion(
    uexecBinaryPath: string | undefined
  ): Promise<string> {
    if (uexecBinaryPath === undefined) {
      return ''
    }

    try {
      await fs.promises.stat(uexecBinaryPath)

      let content = await fs.promises.readFile(uexecBinaryPath, 'utf-8')
      content = content.slice(content.indexOf('\n') + 1) // Skip shebang line
      return YAML.parse(content).platforms[utils.getPlatform()].version
    } catch {
      return ''
    }
  }

  private compareLSPServerVersions(a: string, b: string): number {
    if (!a && b) return -1
    if (a && !b) return 1
    if (!a && !b) return 0

    // Version format is <base>-<pseudo>.
    // Base is standard semantic version (e.g., 0.1.2).
    // Pseudo version includes timestamp/hash (e.g., 20250515195750-abcdef).
    const [baseA, pseudoA = ''] = a.split('-', 2)
    const [baseB, pseudoB = ''] = b.split('-', 2)

    if (semver.gt(baseA, baseB)) return 1
    if (semver.lt(baseA, baseB)) return -1

    if (pseudoA > pseudoB) return 1
    if (pseudoA < pseudoB) return -1
    return 0
  }

  private getLanguageClientOptions(): lc.LanguageClientOptions {
    const ulspConfig = vscode.workspace.getConfiguration(UBER_ULSP_CONFIG)

    const currentLCSettings =
      ulspConfig.get<languageClientSettings>(LANGUAGE_CLIENT_OPTIONS) ?? {}

    const clientOptions: lc.LanguageClientOptions = {
      // Fixed values.
      diagnosticCollectionName: 'ulsp-diag',
      diagnosticPullOptions: {
        onChange: true,
        onSave: true,
        onTabs: true,
      },
      outputChannel: outputChannelClient,
      traceOutputChannel: outputChannelClient,
      markdown: {
        isTrusted: true,
        supportHtml: true,
      },
      errorHandler: {
        error: (
          error: Error,
          message: lc.Message | undefined,
          count: number | undefined
        ) => {
          return {action: lc.ErrorAction.Continue}
        },
        closed: async () => {
          this.restartCount++
          setTimeout(() => {
            this.restartCount = 0
          }, 750)
          if (this.restartCount > 3) {
            return {action: lc.CloseAction.DoNotRestart}
          }
          const running = await this.ensureRunningServer()
          if (running) return {action: lc.CloseAction.Restart}
          return {action: lc.CloseAction.DoNotRestart}
        },
      },

      // Dynamic values directly from settings.
      documentSelector: currentLCSettings.documentSelector,

      // Dynamic values derived from settings via additional logic.
      synchronize: {
        fileEvents: currentLCSettings.fileWatcherPatterns?.map(
          (pattern: string) => {
            return vscode.workspace.createFileSystemWatcher(pattern)
          }
        ),
      },
    }

    return clientOptions
  }

  private customHandlerLogMessage(message: lc.LogMessageParams): void {
    if (message.type <= lc.MessageType.Info) {
      // Message levels Info and higher will force display of the output panel.
      // For longer running tasks, use Log level or below after the first line so the user can choose to hide the panel.
      this.outputChannelServerActions?.show()
    }

    this.outputChannelServerActions?.appendLine(message.message)
  }

  private async quickPickShouldRestartULSP(): Promise<boolean> {
    const selectionItemFullRestart = {
      label: 'Full Restart',
      picked: true,
      description:
        'Recommended option when troubleshooting language intelligence performance.',
      detail:
        "Terminate and restart this machine's language server background process.",
    }
    const selectionItemSessionRestart = {
      label: 'This Window Only',
      detail: "Re-establish this window's language server connection.",
    }
    const selection = await vscode.window.showQuickPick(
      [selectionItemFullRestart, selectionItemSessionRestart],
      {
        placeHolder: 'Select language server restart mode',
        canPickMany: false,
      }
    )

    return selection === selectionItemFullRestart
  }
}
